module github.com/NVIDIA/k8s-dra-driver-gpu

go 1.24.0

toolchain go1.24.2

replace (
	k8s.io/api => github.com/kubernetes/kubernetes/staging/src/k8s.io/api v0.0.0-20250730163427-032142c53e54
	k8s.io/apimachinery => github.com/kubernetes/kubernetes/staging/src/k8s.io/apimachinery v0.0.0-20250730163427-032142c53e54
	k8s.io/client-go => github.com/kubernetes/kubernetes/staging/src/k8s.io/client-go v0.0.0-20250730163427-032142c53e54
	k8s.io/component-base => github.com/kubernetes/kubernetes/staging/src/k8s.io/component-base v0.0.0-20250730163427-032142c53e54
	k8s.io/dynamic-resource-allocation => github.com/kubernetes/kubernetes/staging/src/k8s.io/dynamic-resource-allocation v0.0.0-20250730163427-032142c53e54
	k8s.io/kubelet => github.com/kubernetes/kubernetes/staging/src/k8s.io/kubelet v0.0.0-20250730163427-032142c53e54
	k8s.io/kubernetes => github.com/kubernetes/kubernetes v1.34.0-beta.0.0.20250730163427-032142c53e54
	k8s.io/mount-utils => github.com/kubernetes/kubernetes/staging/src/k8s.io/mount-utils v0.0.0-20250730163427-032142c53e54
)

require (
	github.com/Masterminds/semver v1.5.0
	github.com/NVIDIA/go-nvlib v0.7.4
	github.com/NVIDIA/go-nvml v0.12.9-0
	github.com/NVIDIA/nvidia-container-toolkit v1.18.0-rc.2
	github.com/google/uuid v1.6.0
	github.com/prometheus/client_golang v1.23.0
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/pflag v1.0.7
	github.com/stretchr/testify v1.11.0
	github.com/urfave/cli/v2 v2.27.7
	golang.org/x/sys v0.35.0
	google.golang.org/grpc v1.72.1
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/client-go v0.0.0
	k8s.io/component-base v0.0.0
	k8s.io/dynamic-resource-allocation v0.0.0
	k8s.io/klog/v2 v2.130.1
	k8s.io/kubelet v0.0.0
	k8s.io/kubernetes v0.0.0-00010101000000-000000000000
	k8s.io/mount-utils v0.0.0
	k8s.io/utils v0.0.0-20250604170112-4c0f3b243397
	tags.cncf.io/container-device-interface v1.0.1
	tags.cncf.io/container-device-interface/specs-go v1.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.12.2 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/gnostic-models v0.7.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/runtime-spec v1.2.1 // indirect
	github.com/opencontainers/runtime-tools v0.9.1-0.20221107090550-2e043c6bd626 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.65.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/spf13/cobra v1.9.1 // indirect
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.6.4 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.26.0 // indirect
	golang.org/x/net v0.40.0 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
	golang.org/x/term v0.32.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	golang.org/x/time v0.9.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250303144028-a0af3efb3deb // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250710124328-f3f2b991d03b // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
