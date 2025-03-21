// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package validation_test

import (
	"github.com/gardener/gardener-extension-shoot-dns-service/pkg/apis/service"
	"github.com/gardener/gardener-extension-shoot-dns-service/pkg/apis/service/validation"
	"github.com/gardener/gardener/pkg/apis/core"
	"github.com/gardener/gardener/pkg/utils/test/matchers"

	v1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegatypes "github.com/onsi/gomega/types"
)

var _ = Describe("Validation", func() {
	var (
		awsType     = "aws-route53"
		secretName1 = "my-secret1"
		secretName2 = "my-secret2"
		valid       = []service.DNSProvider{
			{
				Domains:    &service.DNSIncludeExclude{Include: []string{"my.domain.test"}},
				Type:       &awsType,
				SecretName: &secretName1,
			},
			{
				Type:       &awsType,
				SecretName: &secretName2,
			},
		}
		resources = []core.NamedResourceReference{
			{
				Name:        secretName1,
				ResourceRef: v1.CrossVersionObjectReference{},
			},
			{
				Name:        secretName2,
				ResourceRef: v1.CrossVersionObjectReference{},
			},
		}
	)

	DescribeTable("#ValidateDNSConfig",
		func(config service.DNSConfig, resources []core.NamedResourceReference, match gomegatypes.GomegaMatcher) {
			err := validation.ValidateDNSConfig(&config, resources)
			Expect(err).To(match)
		},
		Entry("empty", service.DNSConfig{}, nil, BeEmpty()),
		Entry("valid", service.DNSConfig{
			Providers: valid,
		}, resources, BeEmpty()),
		Entry("missing provider type", service.DNSConfig{
			Providers: modifyCopy(valid[1:], func(items []service.DNSProvider) {
				items[0].Type = nil
			}),
		}, resources, matchers.ConsistOfFields(Fields{
			"Type":   Equal(field.ErrorTypeRequired),
			"Field":  Equal("spec.extensions.[@.type='shoot-dns-service'].providerConfig[0].type"),
			"Detail": Equal("provider type is required"),
		})),
		Entry("invalid provider type", service.DNSConfig{
			Providers: modifyCopy(valid[1:], func(items []service.DNSProvider) {
				t := "dummy"
				items[0].Type = &t
			}),
		}, resources, matchers.ConsistOfFields(Fields{
			"Type":     Equal(field.ErrorTypeInvalid),
			"Field":    Equal("spec.extensions.[@.type='shoot-dns-service'].providerConfig[0].type"),
			"BadValue": Equal("dummy"),
			"Detail":   Equal("unsupported provider type. Valid types are: alicloud-dns, aws-route53, azure-dns, azure-private-dns, cloudflare-dns, google-clouddns, infoblox-dns, netlify-dns, openstack-designate, remote"),
		})),
		Entry("missing secret name", service.DNSConfig{
			Providers: modifyCopy(valid[1:], func(items []service.DNSProvider) {
				items[0].SecretName = nil
			}),
		}, resources, matchers.ConsistOfFields(Fields{
			"Type":   Equal(field.ErrorTypeRequired),
			"Field":  Equal("spec.extensions.[@.type='shoot-dns-service'].providerConfig[0].secretName"),
			"Detail": Equal("secret name is required"),
		})),
		Entry("missing named resource", service.DNSConfig{
			Providers: valid,
		}, resources[1:], matchers.ConsistOfFields(Fields{
			"Type":     Equal(field.ErrorTypeInvalid),
			"Field":    Equal("spec.extensions.[@.type='shoot-dns-service'].providerConfig[0].secretName"),
			"BadValue": Equal("my-secret1"),
			"Detail":   Equal("secret name is not defined as named resource references at 'spec.resources'"),
		})),
	)
})

func modifyCopy(orginal []service.DNSProvider, modifier func([]service.DNSProvider)) []service.DNSProvider {
	var array []service.DNSProvider
	for _, p := range orginal {
		array = append(array, *p.DeepCopy())
	}
	modifier(array)
	return array
}
