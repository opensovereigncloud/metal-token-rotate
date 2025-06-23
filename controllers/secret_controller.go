// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// to be ovverriden in tests
var Now = time.Now

const AutoprovisonAnnotationKey = "metal.ironcore.dev/autoprovision"

type SecretReconciler struct {
	GardenClient client.Client
	LocalClient  client.Client
	Log          logr.Logger
	ConfigPath   string
}

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("name", req.Name, "namespace", req.Namespace)
	config, err := LoadConfig(r.ConfigPath)
	if err != nil {
		log.Error(err, "unable to load config")
		return ctrl.Result{}, err
	}
	var secret corev1.Secret
	if err := r.GardenClient.Get(ctx, req.NamespacedName, &secret); err != nil {
		log.Error(err, "unable to fetch Secret")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	autoprovisionValue, ok := secret.Annotations[AutoprovisonAnnotationKey]
	if !ok {
		log.Info("skkipping secret without autoprovision annotation")
		return ctrl.Result{}, nil
	}
	target, err := parseAutoprovisionValue(autoprovisionValue)
	if err != nil {
		log.Info("skipping secret with invalid autoprovision annotation", "error", err)
		return ctrl.Result{}, nil
	}
	// Find the config for the target identity
	var cfgCluster ClusterConfig
	for _, c := range config.Clusters {
		if c.Identity == target.identity {
			log.Info("found matching config for target identity", "identity", target.identity)
			cfgCluster = c
			break
		}
	}
	if cfgCluster.Identity == "" {
		log.Info("skipping secret without matching config for target identity", "identity", target.identity)
		return ctrl.Result{}, nil
	}
	metalClient := r.LocalClient
	if cfgCluster.TargetSecretName != "" && cfgCluster.TargetSecretNamespace != "" {
		metalClient, err = makeTargetClient(ctx, r.LocalClient, types.NamespacedName{
			Name:      cfgCluster.TargetSecretName,
			Namespace: cfgCluster.TargetSecretNamespace,
		})
		if err != nil {
			log.Error(err, "failed to create metal cluster client")
			return ctrl.Result{}, err
		}
	}
	return r.reconcileInternal(ctx, &secret, ReconcileParams{
		config:          &cfgCluster,
		metalClient:     metalClient,
		targetNamespace: target.namespace,
	})
}

type ReconcileParams struct {
	config          *ClusterConfig
	metalClient     client.Client
	targetNamespace string
}

func (r *SecretReconciler) reconcileInternal(ctx context.Context, secret *corev1.Secret, params ReconcileParams) (ctrl.Result, error) {
	log := r.Log.WithValues("name", secret.Name, "namespace", secret.Namespace)
	unmodifiedSecret := secret.DeepCopy()
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	token, err := r.ensureToken(ctx, ensureTokenParams{
		metalClient: params.metalClient,
		log:         log,
		serviceAccount: types.NamespacedName{
			Name:      params.config.ServiceAccountName,
			Namespace: params.config.ServiceAccountNamespace,
		},
		expirationSecods: params.config.ExpirationSeconds,
		currentToken:     string(secret.Data["token"]),
	})
	if err != nil {
		log.Error(err, "unable to ensure token")
		return ctrl.Result{}, err
	}
	secret.Data["token"] = []byte(token)
	secret.Data["username"] = []byte(params.config.ServiceAccountName)
	secret.Data["namespace"] = []byte(params.targetNamespace)
	err = r.GardenClient.Patch(ctx, secret, client.MergeFrom(unmodifiedSecret))
	if err != nil {
		log.Error(err, "unable to patch Secret")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

type target struct {
	identity  string
	namespace string
}

func parseAutoprovisionValue(value string) (target, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return target{}, fmt.Errorf("invalid autoprovision annotation value: %s", value)
	}
	return target{identity: parts[0], namespace: parts[1]}, nil
}

type ensureTokenParams struct {
	metalClient      client.Client
	log              logr.Logger
	serviceAccount   types.NamespacedName
	expirationSecods int64
	currentToken     string
}

func (r *SecretReconciler) ensureToken(ctx context.Context, params ensureTokenParams) (string, error) {
	needsToken, err := r.needsToken(ctx, params.log, params.currentToken, params.metalClient)
	if err != nil {
		return "", fmt.Errorf("failed to check if token is needed: %w", err)
	}
	if !needsToken {
		return params.currentToken, nil
	}
	var account corev1.ServiceAccount
	account.Name = params.serviceAccount.Name
	account.Namespace = params.serviceAccount.Namespace
	var tokenRequest authenticationv1.TokenRequest
	tokenRequest.Spec.ExpirationSeconds = &params.expirationSecods
	if err := params.metalClient.SubResource("token").Create(ctx, &account, &tokenRequest); err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	r.Log.Info("issued token")
	return tokenRequest.Status.Token, nil
}

type jwtClaims struct {
	Exp int64 `json:"exp"`
	Iat int64 `json:"iat"`
}

func (r *SecretReconciler) needsToken(ctx context.Context, log logr.Logger, currentToken string, metalClient client.Client) (bool, error) {
	if currentToken == "" {
		return true, nil
	}
	var tokenReview authenticationv1.TokenReview
	tokenReview.Spec.Token = currentToken
	if err := metalClient.Create(ctx, &tokenReview); err != nil {
		return false, fmt.Errorf("failed to create token review: %w", err)
	}
	if !tokenReview.Status.Authenticated {
		return true, nil
	}
	parts := strings.Split(currentToken, ".")
	encodedPayload := parts[1]

	decodedPayload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return false, fmt.Errorf("failed to decode payload: %w", err)
	}

	var claims jwtClaims
	err = json.Unmarshal(decodedPayload, &claims)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal claims: %w", err)
	}

	iatTime := time.Unix(claims.Iat, 0)
	expTime := time.Unix(claims.Exp, 0)
	age := Now().Sub(iatTime)
	lifetime := expTime.Sub(iatTime)
	log.Info("token info", "age seconds", age.Seconds(), "lifetime seconds", lifetime.Seconds())
	return age > lifetime/2, nil
}

func makeTargetClient(ctx context.Context, cl client.Client, targetSecret types.NamespacedName) (client.Client, error) {
	var secret corev1.Secret
	err := cl.Get(ctx, targetSecret, &secret)
	if err != nil {
		return nil, err
	}
	configData, ok := secret.Data["kubeconfig"]
	if !ok {
		return nil, errors.New("did not find kubeconfig key in secret")
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(configData)
	if err != nil {
		return nil, err
	}
	return client.New(config, client.Options{Scheme: cl.Scheme()})
}

func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		Complete(r)
}
