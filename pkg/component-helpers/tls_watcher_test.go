/*
Copyright The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package componenthelpers

import (
	"context"
	"crypto/tls"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"kubevirt.io/kubevirt-migration-operator/api/v1alpha1"
)

var _ = Describe("TLS crypto watch", func() {

	Context("GetTLSConfig", func() {
		It("should use Intermediate when the watcher is not ready", func() {
			intermediateCurves := []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveP384}

			cc := NewManagedTLSWatcher().GetTLSConfig(context.Background())
			Expect(cc.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
			Expect(cc.CurvePreferences).To(ConsistOf(intermediateCurves))
		})
	})

	Context("groupsToCurves", func() {
		DescribeTable("should return no curves", func(groups []string) {
			Expect(groupsToCurves(groups)).To(BeEmpty())
		},
			Entry("for a nil list", nil),
			Entry("for an empty list", []string{}),
			Entry("for an empty group", []string{""}),
			Entry("for unknown group names", []string{"foo", "bar"}),
		)

		DescribeTable("should convert", func(group string, curve tls.CurveID) {
			Expect(groupsToCurves([]string{group})).To(ConsistOf(curve))
		},
			EntryDescription("%s to %s"),
			Entry(nil, v1alpha1.TLSGroupX25519, tls.X25519),
			Entry(nil, v1alpha1.TLSGroupSecP256r1, tls.CurveP256),
			Entry(nil, v1alpha1.TLSGroupSecP384r1, tls.CurveP384),
			Entry(nil, v1alpha1.TLSGroupSecP521r1, tls.CurveP521),
			Entry(nil, v1alpha1.TLSGroupX25519MLKEM768, tls.X25519MLKEM768),
			Entry(nil, v1alpha1.TLSGroupSecP256r1MLKEM768, tls.SecP256r1MLKEM768),
			Entry(nil, v1alpha1.TLSGroupSecP384r1MLKEM1024, tls.SecP384r1MLKEM1024),
		)

		It("should skip unknown groups", func() {
			groups := []string{v1alpha1.TLSGroupX25519, "foo", v1alpha1.TLSGroupSecP256r1}
			Expect(groupsToCurves(groups)).To(ConsistOf(tls.X25519, tls.CurveP256))
		})

		It("should map every group of the built-in profiles", func() {
			for profileType, spec := range v1alpha1.TLSProfiles {
				Expect(groupsToCurves(spec.Groups)).To(HaveLen(len(spec.Groups)), "profile %s", profileType)
			}
		})
	})

	Context("getTLSProfileSpec", func() {
		expectSpec := func(tlsSecurityProfile *v1alpha1.TLSSecurityProfile, spec *v1alpha1.TLSProfileSpec) {
			tlsProfileSpec := getTLSProfileSpec(tlsSecurityProfile)
			Expect(tlsProfileSpec.Ciphers).To(Equal(spec.Ciphers))
			Expect(tlsProfileSpec.MinTLSVersion).To(Equal(spec.MinTLSVersion))
			Expect(tlsProfileSpec.Groups).To(Equal(spec.Groups))
		}

		DescribeTable("should fall back to Intermediate for an incomplete profile",
			func(tlsSecurityProfile *v1alpha1.TLSSecurityProfile) {
				expectSpec(tlsSecurityProfile, v1alpha1.TLSProfiles[v1alpha1.TLSProfileIntermediateType])
			},
			Entry("nil profile", nil),
			Entry("empty profile", &v1alpha1.TLSSecurityProfile{}),
			Entry("Custom without a custom spec", &v1alpha1.TLSSecurityProfile{Type: v1alpha1.TLSProfileCustomType}),
		)

		DescribeTable("should return the built-in spec for a known type",
			func(tlsSecurityProfile *v1alpha1.TLSSecurityProfile) {
				expectSpec(tlsSecurityProfile, v1alpha1.TLSProfiles[tlsSecurityProfile.Type])
			},
			Entry("Old", &v1alpha1.TLSSecurityProfile{
				Type: v1alpha1.TLSProfileOldType,
				Old:  &v1alpha1.OldTLSProfile{}}),
			Entry("Modern", &v1alpha1.TLSSecurityProfile{
				Type:   v1alpha1.TLSProfileModernType,
				Modern: &v1alpha1.ModernTLSProfile{}}),
			Entry("Intermediate", &v1alpha1.TLSSecurityProfile{
				Type:         v1alpha1.TLSProfileIntermediateType,
				Intermediate: &v1alpha1.IntermediateTLSProfile{}}),
		)

		It("should return the custom spec for a Custom profile", func() {
			custom := v1alpha1.TLSProfileSpec{
				Ciphers:       []string{"ECDHE-ECDSA-AES128-GCM-SHA256"},
				MinTLSVersion: v1alpha1.VersionTLS11,
				Groups:        []string{v1alpha1.TLSGroupSecP521r1},
			}
			p := &v1alpha1.TLSSecurityProfile{
				Type:   v1alpha1.TLSProfileCustomType,
				Custom: &v1alpha1.CustomTLSProfile{TLSProfileSpec: custom},
			}
			expectSpec(p, &custom)
		})
	})
})
