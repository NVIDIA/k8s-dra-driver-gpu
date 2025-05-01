#!/bin/bash
set -e

mkdir -p certs
cd certs

SERVICE=gpu-mutating-webhook
NAMESPACE=nvidia-dra-driver-gpu
SECRET_NAME=webhook-tls

# Generate the CA key and certificate
openssl genrsa -out ca.key 2048
openssl req -new -x509 -days 365 -key ca.key -subj "/CN=Kubernetes CA" -out ca.crt

# Generate the server key
openssl genrsa -out server.key 2048

# Generate a Certificate Signing Request
cat > csr.conf << EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name

[req_distinguished_name]
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = ${SERVICE}
DNS.2 = ${SERVICE}.${NAMESPACE}
DNS.3 = ${SERVICE}.${NAMESPACE}.svc
EOF

openssl req -new -key server.key -subj "/CN=${SERVICE}.${NAMESPACE}.svc" -out server.csr -config csr.conf

# Sign the certificate
cat > cert.conf << EOF
[auth_ext]
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = ${SERVICE}
DNS.2 = ${SERVICE}.${NAMESPACE}
DNS.3 = ${SERVICE}.${NAMESPACE}.svc
EOF

openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 365 -extfile cert.conf -extensions auth_ext

# Base64 encode the certificates
CA_BUNDLE=$(openssl base64 -A < ca.crt)
TLS_CERT=$(openssl base64 -A < server.crt)
TLS_KEY=$(openssl base64 -A < server.key)

# Create the Secret YAML
cat > webhook-secret.yaml << EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${SECRET_NAME}
  namespace: ${NAMESPACE}
type: kubernetes.io/tls
data:
  tls.crt: ${TLS_CERT}
  tls.key: ${TLS_KEY}
EOF

# Create the webhookconfiguration 
cat > mutatingwebhook.yaml << EOF
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: gpu-mutating-webhook
webhooks:
- name: gpu.mutating.k8s.io
  admissionReviewVersions: ["v1"]
  sideEffects: None
  failurePolicy: Ignore
  clientConfig:
    service:
      name: gpu-mutating-webhook
      namespace: nvidia-dra-driver-gpu
      path: "/mutate"
    caBundle: ${CA_BUNDLE}
  rules:
  - apiGroups: [""]
    apiVersions: ["v1"]
    operations: ["CREATE", "UPDATE"]
    resources: ["pods"]
  namespaceSelector:
    matchExpressions:
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values: ["kube-system", "nvidia-dra-driver-gpu"]
EOF

# Create the resourceclaimtemplate
cat > gpuresourceclaim.yaml << EOF
apiVersion: resource.k8s.io/v1beta1
kind: ResourceClaimTemplate
metadata:
  name: nvidia-gpu-resourceclaim-template
spec:
  spec:
    devices:
      requests:
      - name: gpu
      deviceClassName: gpu.nvidia.com
EOF

echo "Generated TLS certificates and secret successfully"
echo "Apply the secret with: kubectl apply -f webhook-secret.yaml"
echo "Apply the webhook configuration with: kubectl apply -f mutatingwebhook.yaml"
echo "Apply the resourceclaimtemplate with: kubectl apply -f gpuresourceclaim.yaml"
