module github.com/NVIDIA/k8s-dra-driver/deployments/devel

go 1.23.1

require github.com/matryer/moq v0.4.0

replace (
	k8s.io/api v0.32.0 => k8s.io/api v0.29.2
	k8s.io/apiextensions-apiserver v0.32.0 => k8s.io/apiextensions-apiserver v0.29.2
	k8s.io/apimachinery v0.32.0 => k8s.io/apimachinery v0.29.2
)

require (
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/mod v0.22.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/tools v0.29.0 // indirect
)
