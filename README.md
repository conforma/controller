# Conforma Controller

This controller watches for [Tekton](https://tekton.dev/) `PipelineRun` resources that have been signed by [Tekton Chains](https://tekton.dev/docs/chains/) and triggers a validation step using a configurable binary. Once validated, it annotates the `PipelineRun` with the result to prevent duplicate processing.

## ✨ Features

- Watches `PipelineRun` resources across namespaces
- Triggers a `TaskRun` to validate signed images
- Prevents re-processing via annotation
- Compatible with `ko` + `kustomize` workflows
- Secure metrics endpoint exposed via HTTPS
- Minimal RBAC footprint

## 📋 Requirements

- Kubernetes 1.24+
- Tekton Pipelines and Tekton Chains installed
- [ko](https://github.com/ko-build/ko) installed for containerless build/deploys
- [cert-manager](https://cert-manager.io/) (optional, if you use external TLS for metrics)

## 🔧 Behavior

1. The controller listens for `PipelineRun` objects that:
   - Have the annotation: `chains.tekton.dev/signed: "true"`
   - Do **not** yet have the annotation `conforma.dev/validation`

2. For each such `PipelineRun`, it triggers a `TaskRun` that runs a validation binary (you define the image + logic).

3. When the `TaskRun` finishes, it annotates the `PipelineRun` with:

   - `conforma.dev/validation: success` (if `TaskRun` succeeded)
   - `conforma.dev/validation: failure` (if `TaskRun` failed)

4. The controller never processes the same `PipelineRun` twice.

## 🛠 Configuration

| Env Var           | Description                                               |
|-------------------|-----------------------------------------------------------|
| `VALIDATOR_IMAGE` | Container image containing your validation logic          |
| `VALIDATOR_SA`    | (Optional) ServiceAccount name for the validation `TaskRun` |

Set these in the `Deployment` under `config/manager/manager.yaml`.

## 🚀 Deploy

> Replace `ghcr.io/your-org/validator` with your actual validator image

```bash
# Create the target namespace if needed
kubectl create namespace conforma --dry-run=client -o yaml | kubectl apply -f -

# Deploy the controller using ko
KO_DOCKER_REPO=ghcr.io/your-org \
ko apply -f config/default
