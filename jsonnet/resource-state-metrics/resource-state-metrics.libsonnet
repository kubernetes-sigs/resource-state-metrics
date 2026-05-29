// Copyright 2026 The Kubernetes resource-state-metrics Authors.
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

{
  local rsm = self,
  name:: error 'must set name',
  namespace:: error 'must set namespace',
  version:: error 'must set version',
  image:: error 'must set image',

  commonLabels:: {
    'app.kubernetes.io/name': 'resource-state-metrics',
    'app.kubernetes.io/version': rsm.version,
  },

  extraRecommendedLabels:: {
    'app.kubernetes.io/component': 'exporter',
  },

  rbacAggregationLabel:: {
    'resource-state-metrics.instrumentation.k8s-sigs.io/aggregate-to-exporter': 'true'
  },

  podLabels:: {
    [labelName]: rsm.commonLabels[labelName]
    for labelName in std.objectFields(rsm.commonLabels)
    if !std.setMember(labelName, ['app.kubernetes.io/version'])
  },

  aggregatedClusterRoleBinding:
    {
      apiVersion: 'rbac.authorization.k8s.io/v1',
      kind: 'ClusterRoleBinding',
      metadata: {
        name: 'resource-state-metrics-aggregated',
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      roleRef: {
        apiGroup: 'rbac.authorization.k8s.io',
        kind: 'ClusterRole',
        name: 'resource-state-metrics-aggregated',
      },
      subjects: [{
        kind: 'ServiceAccount',
        name: rsm.name,
        namespace: rsm.namespace,
      }],
    },
  clusterRoleBinding:
    {
      apiVersion: 'rbac.authorization.k8s.io/v1',
      kind: 'ClusterRoleBinding',
      metadata: {
        name: rsm.name,
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      roleRef: {
        apiGroup: 'rbac.authorization.k8s.io',
        kind: 'ClusterRole',
        name: rsm.name,
      },
      subjects: [{
        kind: 'ServiceAccount',
        name: rsm.name,
        namespace: rsm.namespace,
      }],
    },

  // ClusterRole rules ordered alphabetically by apiGroup to match controller-gen output.
  clusterRole:
    local rules = [
      {
        apiGroups: ['apiextensions.k8s.io'],
        resources: [
          'customresourcedefinitions',
        ],
        verbs: ['get', 'list', 'watch'],
      },
      {
        apiGroups: ['authentication.k8s.io'],
        resources: [
          'tokenreviews',
        ],
        verbs: ['create'],
      },
      {
        apiGroups: ['authorization.k8s.io'],
        resources: [
          'subjectaccessreviews',
        ],
        verbs: ['create'],
      },
      {
        apiGroups: ['resource-state-metrics.instrumentation.k8s-sigs.io'],
        resources: [
          'resourcemetricsmonitors',
          'resourcemetricsmonitors/status',
        ],
        verbs: ['*'],
      },
    ];

    {
      apiVersion: 'rbac.authorization.k8s.io/v1',
      kind: 'ClusterRole',
      metadata: {
        name: rsm.name,
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      rules: rules,
    },

  // Aggregated ClusterRole enables simple aggregation of permissions to the exporter
  aggregatedClusterRole:
    local aggregationRule = {
      clusterRoleSelectors: [
        {
          matchLabels: rsm.rbacAggregationLabel,
        },
      ],
    };

    {
      apiVersion: 'rbac.authorization.k8s.io/v1',
      kind: 'ClusterRole',
      metadata: {
        name: 'resource-state-metrics-aggregated',
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      aggregationRule: aggregationRule,
    },

  deployment:
    local c = {
      name: 'resource-state-metrics',
      image: rsm.image,
      args: [
        '--main-host=0.0.0.0',
        '--main-port=9999',
        '--self-host=0.0.0.0',
        '--self-port=9998',
      ],
      ports: [
        { name: 'http-metrics', containerPort: 9999 },
        { name: 'telemetry', containerPort: 9998 },
      ],
      securityContext: {
        runAsUser: 65534,
        runAsNonRoot: true,
        allowPrivilegeEscalation: false,
        readOnlyRootFilesystem: true,
        capabilities: { drop: ['ALL'] },
        seccompProfile: { type: 'RuntimeDefault' },
      },
      livenessProbe: { timeoutSeconds: 5, initialDelaySeconds: 5, httpGet: {
        port: 'http-metrics',
        path: '/livez',
      } },
      readinessProbe: { timeoutSeconds: 5, initialDelaySeconds: 5, httpGet: {
        port: 'telemetry',
        path: '/readyz',
      } },
    };

    {
      apiVersion: 'apps/v1',
      kind: 'Deployment',
      metadata: {
        name: rsm.name,
        namespace: rsm.namespace,
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      spec: {
        replicas: 1,
        selector: { matchLabels: rsm.podLabels },
        template: {
          metadata: {
            labels: rsm.commonLabels + rsm.extraRecommendedLabels,
          },
          spec: {
            containers: [c],
            serviceAccountName: rsm.serviceAccount.metadata.name,
            automountServiceAccountToken: true,
            nodeSelector: { 'kubernetes.io/os': 'linux' },
          },
        },
      },
    },

  serviceAccount:
    {
      apiVersion: 'v1',
      kind: 'ServiceAccount',
      metadata: {
        name: rsm.name,
        namespace: rsm.namespace,
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      automountServiceAccountToken: false,
    },

  service:
    {
      apiVersion: 'v1',
      kind: 'Service',
      metadata: {
        name: rsm.name,
        namespace: rsm.namespace,
        labels: rsm.commonLabels + rsm.extraRecommendedLabels,
      },
      spec: {
        clusterIP: 'None',
        selector: rsm.podLabels,
        ports: [
          { name: 'http-metrics', port: 9999, targetPort: 'http-metrics' },
          { name: 'telemetry', port: 9998, targetPort: 'telemetry' },
        ],
      },
    },
}
