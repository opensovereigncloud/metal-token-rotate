// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	certutil "k8s.io/client-go/util/cert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ironcore-dev/metal-token-rotate/controllers"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

func main() {
	var kubecontext string
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	flag.StringVar(&kubecontext, "kubecontext", "", "The context to use from the kubeconfig (defaults to current-context)")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	localConfig := getKubeconfigOrDie(kubecontext)
	setupLog.Info("loaded local kubeconfig", "context", kubecontext, "host", localConfig.Host)

	gardenClusterAddress := os.Getenv("GARDEN_CLUSTER_ADDRESS")
	gardenConfig, err := gardenClusterConfig(gardenClusterAddress)
	if err != nil {
		setupLog.Error(err, "Failed to load garden cluster config")
		os.Exit(1)
	}
	localClient, err := client.New(localConfig, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "Failed to create garden client")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(gardenConfig, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
	})
	if err != nil {
		setupLog.Error(err, "unable to setup manager")
		os.Exit(1)
	}

	secretController := controllers.SecretReconciler{
		GardenClient: mgr.GetClient(),
		LocalClient:  localClient,
		Log:          ctrl.Log.WithName("controllers").WithName("secret"),
		ConfigPath:   controllers.DefaultConfigPath,
	}
	if err = secretController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Secret")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
	setupLog.Info("received SIGTERM or SIGINT. See you later.")
}

func getKubeconfigOrDie(kubecontext string) *rest.Config {
	if kubecontext == "" {
		kubecontext = os.Getenv("KUBECONTEXT")
	}
	restConfig, err := ctrlconfig.GetConfigWithContext(kubecontext)
	if err != nil {
		setupLog.Error(err, "Failed to load kubeconfig")
		os.Exit(1)
	}
	return restConfig
}

func gardenClusterConfig(apiAddress string) (*rest.Config, error) {
	const (
		tokenFile  = "/var/run/garden/token/token" //nolint:gosec
		rootCAFile = "/var/run/garden/ca/bundle.crt"
	)

	if apiAddress == "" {
		return nil, errors.New("garden api address is empty")
	}

	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, err
	}

	tlsClientConfig := rest.TLSClientConfig{}

	if _, err := certutil.NewPool(rootCAFile); err != nil {
		return nil, fmt.Errorf("expected to load root CA config from %s, but got err: %w", rootCAFile, err)
	} else {
		tlsClientConfig.CAFile = rootCAFile
	}

	return &rest.Config{
		Host:            apiAddress,
		TLSClientConfig: tlsClientConfig,
		BearerToken:     string(token),
		BearerTokenFile: tokenFile,
	}, nil
}
