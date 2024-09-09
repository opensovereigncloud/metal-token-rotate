// Copyright 2024 SAP SE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

	"github.com/ironcore-dev/metal-token-dealer/controllers"
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
	metalClient, err := client.New(localConfig, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "Failed to create garden client")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(gardenConfig, ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   true,
		LeaderElectionID: "runtime-extension-maintenance-controller",
	})
	if err != nil {
		setupLog.Error(err, "unable to setup manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	secretController := controllers.SecretReconciler{
		MetalClient:  metalClient,
		GardenClient: mgr.GetClient(),
		Log:          ctrl.Log.WithName("controllers").WithName("Secret"),
		ConfigPath:   controllers.DefaultConfigPath,
	}
	if err = secretController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Secret")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
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
		tokenFile  = "/var/run/garden/token" //nolint:gosec
		rootCAFile = "/var/run/garden/ca.crt"
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
