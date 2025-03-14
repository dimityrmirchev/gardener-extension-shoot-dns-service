// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mutator

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/gardener/gardener-extension-shoot-dns-service/pkg/admission/common"
	servicev1alpha1 "github.com/gardener/gardener-extension-shoot-dns-service/pkg/apis/service/v1alpha1"
	pkgservice "github.com/gardener/gardener-extension-shoot-dns-service/pkg/service"
	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewShootMutator returns a new instance of a shoot mutator.
func NewShootMutator() extensionswebhook.Mutator {
	return &shoot{}
}

// shoot mutates shoots
type shoot struct {
	common.ShootAdmissionHandler
	lock    sync.Mutex
	encoder runtime.Encoder
}

// Mutate implements extensionswebhook.Mutator.Mutate
func (s *shoot) Mutate(ctx context.Context, new, old client.Object) error {
	shoot, ok := new.(*gardencorev1beta1.Shoot)
	if !ok {
		return fmt.Errorf("wrong object type %T", new)
	}

	var oldShoot *gardencorev1beta1.Shoot
	if old != nil {
		var ok bool
		oldShoot, ok = old.(*gardencorev1beta1.Shoot)
		if !ok {
			return fmt.Errorf("wrong object type %T for old object", old)
		}
	}

	return s.mutateShoot(ctx, oldShoot, shoot)
}

func (s *shoot) mutateShoot(_ context.Context, _, new *gardencorev1beta1.Shoot) error {
	if s.isDisabled(new) {
		return nil
	}
	dnsConfig, err := s.extractDNSConfig(new)
	if err != nil {
		return err
	}

	syncProviders := dnsConfig == nil || dnsConfig.Providers == nil
	if dnsConfig != nil && dnsConfig.SyncProvidersFromShootSpecDNS != nil {
		syncProviders = *dnsConfig.SyncProvidersFromShootSpecDNS
	}
	if !syncProviders {
		return nil
	}

	if dnsConfig == nil {
		dnsConfig = &servicev1alpha1.DNSConfig{}
	}
	dnsConfig.SyncProvidersFromShootSpecDNS = &syncProviders

	oldNamedResources := map[string]int{}
	for i, r := range new.Spec.Resources {
		oldNamedResources[r.Name] = i
	}

	dnsConfig.Providers = nil
	for _, p := range new.Spec.DNS.Providers {
		np := servicev1alpha1.DNSProvider{Type: p.Type}
		if p.Domains != nil {
			np.Domains = &servicev1alpha1.DNSIncludeExclude{
				Include: p.Domains.Include,
				Exclude: p.Domains.Exclude,
			}
		}
		if p.Zones != nil {
			np.Zones = &servicev1alpha1.DNSIncludeExclude{
				Include: p.Zones.Include,
				Exclude: p.Zones.Exclude,
			}
		}
		if p.SecretName != nil {
			secretName := pkgservice.ExtensionType + "-" + *p.SecretName
			np.SecretName = &secretName
			resource := gardencorev1beta1.NamedResourceReference{
				Name: secretName,
				ResourceRef: autoscalingv1.CrossVersionObjectReference{
					Kind:       "Secret",
					Name:       *p.SecretName,
					APIVersion: "v1",
				},
			}
			if index, ok := oldNamedResources[secretName]; ok {
				new.Spec.Resources[index].ResourceRef = resource.ResourceRef
			} else {
				new.Spec.Resources = append(new.Spec.Resources, resource)
			}
		}
		dnsConfig.Providers = append(dnsConfig.Providers, np)
	}

	return s.updateDNSConfig(new, dnsConfig)
}

// isDisabled returns true if extension is explicitly disabled.
func (s *shoot) isDisabled(shoot *gardencorev1beta1.Shoot) bool {
	if shoot.Spec.DNS == nil {
		return true
	}
	if shoot.DeletionTimestamp != nil {
		// don't mutate shoots in deletion
		return true
	}
	ext := s.findExtension(shoot)
	if ext == nil {
		return false
	}
	if ext.Disabled != nil {
		return *ext.Disabled
	}
	return false
}

// extractDNSConfig extracts DNSConfig from providerConfig.
func (s *shoot) extractDNSConfig(shoot *gardencorev1beta1.Shoot) (*servicev1alpha1.DNSConfig, error) {
	ext := s.findExtension(shoot)
	if ext != nil && ext.ProviderConfig != nil && ext.ProviderConfig.Raw != nil {
		dnsConfig := &servicev1alpha1.DNSConfig{}
		if _, _, err := s.GetDecoder().Decode(ext.ProviderConfig.Raw, nil, dnsConfig); err != nil {
			return nil, fmt.Errorf("failed to decode %s provider config: %w", ext.Type, err)
		}
		return dnsConfig, nil
	}

	return nil, nil
}

// findExtension returns shoot-dns-service extension.
func (s *shoot) findExtension(shoot *gardencorev1beta1.Shoot) *gardencorev1beta1.Extension {
	if shoot.Spec.DNS == nil {
		return nil
	}
	for i, ext := range shoot.Spec.Extensions {
		if ext.Type == pkgservice.ExtensionType {
			return &shoot.Spec.Extensions[i]
		}
	}
	return nil
}

func (s *shoot) updateDNSConfig(shoot *gardencorev1beta1.Shoot, config *servicev1alpha1.DNSConfig) error {
	raw, err := s.toRaw(config)
	if err != nil {
		return err
	}

	index := -1
	for i, ext := range shoot.Spec.Extensions {
		if ext.Type == pkgservice.ExtensionType {
			index = i
			break
		}
	}
	if index == -1 {
		index = len(shoot.Spec.Extensions)
		shoot.Spec.Extensions = append(shoot.Spec.Extensions, gardencorev1beta1.Extension{
			Type: pkgservice.ExtensionType,
		})
	}
	shoot.Spec.Extensions[index].ProviderConfig = &runtime.RawExtension{Raw: raw}
	return nil
}

func (s *shoot) toRaw(config *servicev1alpha1.DNSConfig) ([]byte, error) {
	encoder, err := s.getEncoder()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	if err := encoder.Encode(config, &b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *shoot) getEncoder() (runtime.Encoder, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.encoder != nil {
		return s.encoder, nil
	}

	codec := s.NewCodecFactory()
	si, ok := runtime.SerializerInfoForMediaType(codec.SupportedMediaTypes(), runtime.ContentTypeJSON)
	if !ok {
		return nil, fmt.Errorf("could not find encoder for media type %q", runtime.ContentTypeJSON)
	}
	s.encoder = codec.EncoderForVersion(si.Serializer, servicev1alpha1.SchemeGroupVersion)
	return s.encoder, nil
}
