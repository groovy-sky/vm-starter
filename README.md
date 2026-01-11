# VMStarter

VMStarter is a Go-based worker whose only job is to iterate over every Azure subscription visible to its identity, enumerate all virtual machines, and issue a `POST .../start` call for each VM. Packaging it as an Azure Container Apps (ACA) Job lets you fan-out VM start operations on demand or via schedule without wiring up custom automation per subscription.

## What it is for

1. **Fleet warm-up / DR readiness:** Quickly bring entire VM estates online ahead of business hours, load tests, or disaster-recovery drills.
2. **Consistency across subscriptions:** Instead of juggling per-subscription runbooks, the binary discovers subscriptions dynamically via ARM.
3. **Container-first operations:** Because the binary is a statically linked Go app, it runs reliably as an ACA Job and emits simple `[INF]`/`[ERR]` logs for observability.

## How to use it to spin an Azure Container Apps Job

### 1. Gather prerequisites

- Azure CLI `>= 2.53.0` with the Container Apps extension: `az extension add --name containerapp`.
- Permission to create resource groups, container registries, Container Apps environments, and managed identities in the deployment subscription.
- Docker or another OCI builder to produce the container image.

### 2. Build the image and push to ACR

```bash
cd Containers/VMStarter

docker build -t vmstarter:local .

RESOURCE_GROUP=rg-vmstarter-aca
LOCATION=westeurope
REGISTRY_NAME=vmstarterregistry
IMAGE_TAG=${REGISTRY_NAME}.azurecr.io/vmstarter:1.0.0

az group create -n $RESOURCE_GROUP -l $LOCATION
az acr create -n $REGISTRY_NAME -g $RESOURCE_GROUP --sku Basic
az acr login -n $REGISTRY_NAME

docker tag vmstarter:local $IMAGE_TAG
docker push $IMAGE_TAG
```

### 3. Create the ACA environment

```bash
ENVIRONMENT=vmstarter-env

az containerapp env create \
  -n $ENVIRONMENT \
  -g $RESOURCE_GROUP \
  --location $LOCATION \
  --logs-workspace new
```

This provisions a Log Analytics workspace so you can query job logs later.

### 4. Provision (and authorize) a managed identity

```bash
IDENTITY_NAME=vmstarter-job-mi
az identity create -g $RESOURCE_GROUP -n $IDENTITY_NAME
IDENTITY_ID=$(az identity show -g $RESOURCE_GROUP -n $IDENTITY_NAME --query id -o tsv)

# Assign required roles for every subscription that should be started
TARGET_SUBSCRIPTION=<subscription-id>
az role assignment create \
  --assignee $IDENTITY_ID \
  --role "Reader" \
  --scope /subscriptions/${TARGET_SUBSCRIPTION}

az role assignment create \
  --assignee $IDENTITY_ID \
  --role "Virtual Machine Contributor" \
  --scope /subscriptions/${TARGET_SUBSCRIPTION}
```

### 5. Create the Container Apps Job

```bash
JOB_NAME=vmstarter-job
REGISTRY_PASSWORD=$(az acr credential show -n $REGISTRY_NAME --query passwords[0].value -o tsv)
REGISTRY_USERNAME=$(az acr credential show -n $REGISTRY_NAME --query username -o tsv)

az containerapp job create \
  -g $RESOURCE_GROUP \
  -n $JOB_NAME \
  --environment $ENVIRONMENT \
  --trigger-type Manual \
  --replica-timeout 600 \
  --replica-retry-limit 1 \
  --replica-completion-count 1 \
  --image $IMAGE_TAG \
  --registry-server ${REGISTRY_NAME}.azurecr.io \
  --registry-username $REGISTRY_USERNAME \
  --registry-password $REGISTRY_PASSWORD \
  --cpu 0.25 --memory 0.5Gi \
  --identity resource-id=$IDENTITY_ID \
  --name-servers 168.63.129.16
```

Key switches:
- `--trigger-type Manual` keeps executions on-demand until you apply a schedule.
- `--replica-*` options ensure one replica per run with limited retry.
- `--identity` attaches the previously authorized managed identity so the app can authenticate through `DefaultAzureCredential`.

### 6. Run or schedule the job

```bash
# Start immediately
az containerapp job start -g $RESOURCE_GROUP -n $JOB_NAME

# Inspect executions and logs
az containerapp job execution list -g $RESOURCE_GROUP -n $JOB_NAME
EXECUTION_ID=$(az containerapp job execution list -g $RESOURCE_GROUP -n $JOB_NAME --query "[0].name" -o tsv)
az containerapp job execution logs show -g $RESOURCE_GROUP -n $JOB_NAME --execution $EXECUTION_ID

# Optional CRON schedule (every day 05:00 UTC)
az containerapp job update \
  -g $RESOURCE_GROUP \
  -n $JOB_NAME \
  --trigger-type Schedule \
  --schedule-expression "0 5 * * *"
```

## Required permissions for the app

The managed identity (or workload identity) that runs VMStarter must have:

| Scope | Role | Why |
|-------|------|-----|
| Subscription (each target) | Reader | Allows the job to enumerate the subscription and list VMs via ARM. |
| Subscription (each target) | Virtual Machine Contributor *or* the `Microsoft.Compute/virtualMachines/start/action` custom role | Grants rights to invoke the VM `start` action. |

Optional but recommended permissions:
- **Monitoring Reader** on the Log Analytics workspace to inspect logs from a separate principal.
- **AcrPull** on the container registry if you are not using admin/user credentials at job creation time.

Remember to scope assignments narrowly. If you only need to start VMs in specific resource groups, grant the roles at the resource-group scope instead of whole subscriptions.

## Local build and test

```bash
# Authenticate once so DefaultAzureCredential can reuse your session
az login

# Export service principal secrets only when needed
export AZURE_TENANT_ID=<tenant>
export AZURE_CLIENT_ID=<client-if-using-uami-or-sp>
export AZURE_CLIENT_SECRET=<secret-if-using-sp>

go build ./...
go run .
```

Run the container locally (assumes the environment variables above or an Azure CLI login exist on the host):

```bash
docker run --rm \
  -e AZURE_TENANT_ID \
  -e AZURE_CLIENT_ID \
  -e AZURE_CLIENT_SECRET \
  vmstarter:local
```

## How the code works (`main.go`)

| Area | Purpose |
|------|---------|
| `getAzureAccessToken` | Requests an ARM bearer token via `azidentity.NewDefaultAzureCredential`, enabling managed identity, workload identity, or developer logins without code changes. |
| `sendRequest` | Issues HTTP calls with the bearer token, JSON headers, and a 30-second timeout. |
| `parseResourceGroup` | Splits the VM resource ID to locate the resource group name required for the `start` call. |
| `main` | Fetches subscriptions, enumerates VMs, stamps the resource group, sends `POST .../start`, and logs `[INF]`/`[ERR]` events for each outcome. |

The code talks directly to `https://management.azure.com` using API versions `2022-12-01` (subscriptions) and `2025-04-01` (VMs), so no SDK-generated clients are required.

## Monitoring, diagnostics, and cleanup

- **Logs:** `az containerapp job execution logs show` or Log Analytics queries (the ACA environment creates a workspace by default).
- **Activity Log:** Every VM start is recorded under the managed identity for compliance/audit.
- **Cleanup:** `az group delete -n $RESOURCE_GROUP --yes --no-wait` removes the job, identity, ACR, and environment.

> If you need to inspect the deployed production instance, use: `az resource show --ids /subscriptions/5e00f86c-4597-47d6-a4e9-45d2addda91a/resourceGroups/PROD-HUB-WE-VM/providers/Microsoft.App/jobs/prod-hub-we-vm-starter`.
