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
	"fmt"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-operator/api/v1alpha1"
)

var (
	tlsVersionMap = map[string]uint16{
		"VersionTLS10": tls.VersionTLS10,
		"VersionTLS11": tls.VersionTLS11,
		"VersionTLS12": tls.VersionTLS12,
		"VersionTLS13": tls.VersionTLS13,
	}
	cipherSuites      = tls.CipherSuites()
	extraCipherSuites = map[string]uint16{
		"ECDHE-ECDSA-AES128-GCM-SHA256": tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		"ECDHE-RSA-AES128-GCM-SHA256":   tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		"ECDHE-ECDSA-AES256-GCM-SHA384": tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		"ECDHE-RSA-AES256-GCM-SHA384":   tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		"ECDHE-ECDSA-CHACHA20-POLY1305": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		"ECDHE-RSA-CHACHA20-POLY1305":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		"ECDHE-ECDSA-AES128-SHA256":     tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
		"ECDHE-RSA-AES128-SHA256":       tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
		"ECDHE-ECDSA-AES128-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		"ECDHE-RSA-AES128-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		"ECDHE-ECDSA-AES256-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		"ECDHE-RSA-AES256-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		"AES128-GCM-SHA256":             tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		"AES256-GCM-SHA384":             tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		"AES128-SHA256":                 tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
		"AES128-SHA":                    tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		"AES256-SHA":                    tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		"DES-CBC3-SHA":                  tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	}
	cipherNameToID = func() map[string]uint16 {
		m := make(map[string]uint16, len(cipherSuites)+len(extraCipherSuites))
		for k, v := range extraCipherSuites {
			m[k] = v
		}
		for _, cs := range cipherSuites {
			m[cs.Name] = cs.ID
		}
		return m
	}()

	log = ctrl.Log.WithName("managed-tls-watcher")
)

type cryptoConfig struct {
	CipherSuites []uint16
	MinVersion   uint16
}

type ManagedTLSWatcher struct {
	mu            sync.RWMutex
	cache         cache.Cache
	defaultConfig *cryptoConfig
	ready         bool
}

func NewManagedTLSWatcher() *ManagedTLSWatcher {
	return &ManagedTLSWatcher{
		defaultConfig: cryptoConfigFromSpec(nil),
	}
}

func (m *ManagedTLSWatcher) SetCache(c cache.Cache) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = c
}

func (m *ManagedTLSWatcher) Start(ctx context.Context) error {
	m.mu.RLock()
	c := m.cache
	m.mu.RUnlock()

	if c == nil {
		return fmt.Errorf("no cache provided for tls watcher")
	}
	log.Info("ManagedTLSWatcher: starting, waiting for cache sync")
	if !c.WaitForCacheSync(ctx) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	list := &migrationsv1alpha1.MigControllerList{}
	if err := c.List(ctx, list); err != nil {
		log.Info("MigController CRD not available, using default TLS configuration", "error", err)
		<-ctx.Done()
		return nil
	}

	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
	log.Info("ManagedTLSWatcher: ready")

	<-ctx.Done()
	return nil
}

func (m *ManagedTLSWatcher) NeedLeaderElection() bool {
	return false
}

func (m *ManagedTLSWatcher) GetTLSConfig(ctx context.Context) *cryptoConfig {
	m.mu.RLock()
	ready := m.ready
	c := m.cache
	m.mu.RUnlock()

	if !ready || c == nil {
		return m.defaultConfig
	}

	list := &migrationsv1alpha1.MigControllerList{}
	if err := c.List(ctx, list); err != nil || len(list.Items) == 0 {
		return m.defaultConfig
	}

	return cryptoConfigFromSpec(list.Items[0].Spec.TLSSecurityProfile)
}

func cryptoConfigFromSpec(profile *migrationsv1alpha1.TLSSecurityProfile) *cryptoConfig {
	cipherNames, minTypedTLSVersion := selectCipherSuitesAndMinTLSVersion(profile)
	minTLSVersion, ok := tlsVersionMap[string(minTypedTLSVersion)]
	if !ok {
		log.Info("unknown TLS version %q, defaulting to TLS 1.2", minTypedTLSVersion)
		minTLSVersion = tls.VersionTLS12
	}
	return &cryptoConfig{
		CipherSuites: cipherSuitesIDs(cipherNames),
		MinVersion:   minTLSVersion,
	}
}

func selectCipherSuitesAndMinTLSVersion(
	profile *migrationsv1alpha1.TLSSecurityProfile,
) ([]string, migrationsv1alpha1.TLSProtocolVersion) {
	if profile == nil {
		profile = &migrationsv1alpha1.TLSSecurityProfile{
			Type:         migrationsv1alpha1.TLSProfileIntermediateType,
			Intermediate: &migrationsv1alpha1.IntermediateTLSProfile{},
		}
	}
	if profile.Custom != nil {
		return profile.Custom.TLSProfileSpec.Ciphers, profile.Custom.TLSProfileSpec.MinTLSVersion
	}
	return migrationsv1alpha1.TLSProfiles[profile.Type].Ciphers, migrationsv1alpha1.TLSProfiles[profile.Type].MinTLSVersion
}

func cipherSuitesIDs(names []string) []uint16 {
	var ids []uint16
	for _, name := range names {
		if id, ok := cipherNameToID[name]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}
