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

package controllers_test

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ironcore-dev/metal-token-dealer/controllers"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

const (
	serviceAccountName string = "test-service-account"
	identity           string = "test-cluster"
)

var (
	metalEnv  *envtest.Environment
	gardenEnv *envtest.Environment

	metalClient  client.Client
	gardenClient client.Client

	stopController context.CancelFunc
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	Expect(corev1.AddToScheme(clientgoscheme.Scheme)).To(Succeed())

	By("bootstrapping metal cluster")
	metalEnv = &envtest.Environment{}
	metalCfg, err := metalEnv.Start()
	Expect(err).To(Succeed())
	Expect(metalCfg).ToNot(BeNil())
	metalClient, err = client.New(metalCfg, client.Options{})
	Expect(err).To(Succeed())

	var serviceAccount corev1.ServiceAccount
	serviceAccount.Name = serviceAccountName
	serviceAccount.Namespace = metav1.NamespaceDefault
	Expect(metalClient.Create(context.Background(), &serviceAccount)).To(Succeed())

	By("bootstrapping garden cluster")
	gardenEnv = &envtest.Environment{}
	gardenCfg, err := gardenEnv.Start()
	Expect(err).To(Succeed())
	Expect(gardenCfg).ToNot(BeNil())
	gardenClient, err = client.New(gardenCfg, client.Options{})
	Expect(err).To(Succeed())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	stopController = cancel

	mgr, err := ctrl.NewManager(gardenCfg, ctrl.Options{
		Scheme: clientgoscheme.Scheme,
	})
	Expect(err).To(Succeed())

	configPath := "test.json"
	reconciler := &controllers.SecretReconciler{
		MetalClient:  metalClient,
		GardenClient: gardenClient,
		Log:          GinkgoLogr,
		ConfigPath:   configPath,
	}
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	config := controllers.Config{
		ServiceAccountName:      serviceAccount.Name,
		ServiceAccountNamespace: serviceAccount.Namespace,
		ExpirationSeconds:       600,
		Identity:                identity,
	}
	data, err := json.Marshal(config)
	Expect(err).To(Succeed())
	Expect(os.WriteFile(configPath, data, 0644)).To(Succeed())

	go func() {
		err := mgr.Start(ctx)
		Expect(err).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	stopController()
	Expect(os.Remove("test.json")).To(Succeed())
	By("tearing down the garden cluster")
	Expect(gardenEnv.Stop()).To(Succeed())
	By("tearing down the metal cluster")
	Expect(metalEnv.Stop()).To(Succeed())
})
