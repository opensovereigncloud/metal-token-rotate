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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ironcore-dev/metal-token-rotate/controllers"
)

var _ = Describe("The secret controller", func() {

	var secret *corev1.Secret

	BeforeEach(func() {
		controllers.Now = time.Now
		secret = &corev1.Secret{}
		secret.Namespace = metav1.NamespaceDefault
	})

	AfterEach(func(ctx SpecContext) {
		Expect(client.IgnoreNotFound(gardenClient.Delete(ctx, secret))).To(Succeed())
	})

	It("injects a token into an autoprovisioned secret", func(ctx SpecContext) {
		secret.Name = "test-secret-inject"
		secret.Annotations = map[string]string{controllers.AutoprovisonAnnotationKey: identity + "/server-namespace"}
		Expect(gardenClient.Create(ctx, secret)).To(Succeed())

		Eventually(func() map[string][]byte {
			var result corev1.Secret
			Expect(gardenClient.Get(ctx, client.ObjectKeyFromObject(secret), &result)).To(Succeed())
			return result.Data
		}).Should(SatisfyAll(
			HaveKeyWithValue("token", Not(BeNil())),
			HaveKeyWithValue("namespace", BeEquivalentTo("server-namespace")),
			HaveKeyWithValue("username", BeEquivalentTo(serviceAccountName)),
		))
	})

	It("rotates the token in an autoprovisioned secret", func(ctx SpecContext) {
		secret.Name = "test-secret-rotate"
		secret.Annotations = map[string]string{controllers.AutoprovisonAnnotationKey: identity + "/server-namespace"}
		Expect(gardenClient.Create(ctx, secret)).To(Succeed())

		controllers.Now = func() time.Time {
			return time.Now().Add(20 * time.Minute)
		}

		var oldToken []byte
		Eventually(func() []byte {
			var result corev1.Secret
			Expect(gardenClient.Get(ctx, client.ObjectKeyFromObject(secret), &result)).To(Succeed())
			oldToken = result.Data["token"]
			return oldToken
		}).ShouldNot(BeNil())

		// force reconciliation
		unmodifiedSecret := secret.DeepCopy()
		unmodifiedSecret.Labels = map[string]string{"a": "b"}
		Expect(gardenClient.Patch(ctx, secret, client.MergeFrom(unmodifiedSecret))).To(Succeed())

		Eventually(func() []byte {
			var result corev1.Secret
			Expect(gardenClient.Get(ctx, client.ObjectKeyFromObject(secret), &result)).To(Succeed())
			return result.Data["token"]
		}).ShouldNot(Equal(oldToken))
	})

	It("does not inject a token into a secret without the autoprovision annotation", func(ctx SpecContext) {
		secret.Name = "test-secret-no-annotation"
		Expect(gardenClient.Create(ctx, secret)).To(Succeed())

		Consistently(func() map[string][]byte {
			var result corev1.Secret
			Expect(gardenClient.Get(ctx, client.ObjectKeyFromObject(secret), &result)).To(Succeed())
			return result.Data
		}).Should(BeEmpty())
	})

	It("does not inject a token into a secret with an invalid autoprovision annotation", func(ctx SpecContext) {
		secret.Name = "test-secret-invalid-annotation"
		secret.Annotations = map[string]string{controllers.AutoprovisonAnnotationKey: "invalid"}
		Expect(gardenClient.Create(ctx, secret)).To(Succeed())

		Consistently(func() map[string][]byte {
			var result corev1.Secret
			Expect(gardenClient.Get(ctx, client.ObjectKeyFromObject(secret), &result)).To(Succeed())
			return result.Data
		}).Should(BeEmpty())
	})

})
