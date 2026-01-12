# VMStarter

![](/logo_small.png)

## Introduction

Azure have built-in VM auto-shutdown feature, that stops a virtual machine at specified time and can optionally send a notification (email or webhook) before the shutdown happens.

There is no automatic start features for VMs in Azure. If you want the VM to come back on by itself at a certain time, you need a separate “start VM on a schedule” solution. This repository have a Golang app named VMStarter, which allows to start VM using Azure REST API calls.

## How it works?

VMStarter is a Go-based worker whose only job is to iterate over every Azure subscription visible to its identity, enumerate all virtual machines, and send POST request for each VM to start it. On top of that you can use an Azure Container Apps (ACA) Job for VM start operations on demand or via schedule without wiring up custom automation per subscription.

## How to run it?

To run the app you can use:
* The implementation in [main.go](/main.go).  
* Container image in [Dockerfile](/Dockerfile).  
* Built and ready-to-use [Docker Hub image](https://hub.docker.com/repository/docker/gr00vysky/vm-starter)

## Running Container App Job

This section explains how-to run VMStarter by using Azure Container Apps Job. Container App Job will use a managed identity and must have "Reader" and "Virtual Machine Contributor" (or custom role with `Microsoft.Compute/virtualMachines/start/action` permission) on required VM to start it. By default, in [the deployment script](#deployment-script), access will be granted to the whole default subscription.

If you only need to start VMs in specific resource groups, grant the roles at the resource-group scope instead of whole subscriptions.

### Prerequisites

- Azure CLI
- Permission to create resource groups, Container Apps environments, and managed identities in the deployment subscription.
- Docker or another OCI builder to produce the container image.

### Deployment script

The following script provisions a minimal end-to-end setup:

- Creates a resource group and a Container Apps Environment.
- Creates a user-assigned managed identity and captures its principal ID.
- Grants the identity the required permissions on the target subscription(s) so the job can enumerate VMs and start them.
- Creates an Azure Container Apps **Job** triggered by a cron schedule and configured to run a single replica to completion.

Notes:
- Replace `TARGET_SUBSCRIPTION` (and/or repeat the role assignments) if you want the job to operate across multiple subscriptions.
- Adjust `--cron-expression` to match your desired start time and timezone expectations.
- Tune `--replica-timeout`, `--cpu`, and `--memory` based on the number of subscriptions/VMs you need to process.

```bash
IMAGE_REG=docker.io
IMAGE_TAG=gr00vysky/vm-starter:latest
RESOURCE_GROUP=vmstarter-rg
LOCATION=westeurope
ENVIRONMENT=$RESOURCE_GROUP-env
JOB_NAME=$RESOURCE_GROUP-cron

# Resource group deployment
az group create -n $RESOURCE_GROUP -l $LOCATION

az containerapp env create \
  -n $ENVIRONMENT \
  -g $RESOURCE_GROUP \
  --location $LOCATION \

# Provision (and authorize) a managed identity
IDENTITY_NAME=$ENVIRONMENT-ID
az identity create -g $RESOURCE_GROUP -n $IDENTITY_NAME
IDENTITY_ID=$(az identity show -g $RESOURCE_GROUP -n $IDENTITY_NAME --query principalId -o tsv)

# Assign required roles for every subscription that should be started
TARGET_SUBSCRIPTION=$(az account list --query "[?isDefault].id | [0]" -o tsv)
az role assignment create \
  --assignee $IDENTITY_ID \
  --role "Reader" \
  --scope /subscriptions/${TARGET_SUBSCRIPTION}

az role assignment create \
  --assignee $IDENTITY_ID \
  --role "Virtual Machine Contributor" \
  --scope /subscriptions/${TARGET_SUBSCRIPTION}

# Create the Container Apps Job
az containerapp job create \
  -g $RESOURCE_GROUP \
  -n $JOB_NAME \
  --environment $ENVIRONMENT \
  --trigger-type Schedule \
  --cron-expression "0 7 * * 1-5" \
  --replica-timeout 600 \
  --parallelism 1 \
  --replica-retry-limit 1 \
  --replica-completion-count 1 \
  --image $IMAGE_TAG \
  --cpu 0.25 --memory 0.5Gi
```

### Result

After running the script, you should have:

- A Container Apps Environment and a scheduled Container Apps Job in the resource group.
- A managed identity assigned with permissions to read VM inventory and start VMs in the selected subscription scope.
- A job that executes on the configured cron schedule (example above: **07:00, Monday–Friday**) and runs the VMStarter container once per schedule tick.

You can validate the setup by checking job executions and logs:

- View job executions:
  - Azure Portal → Container Apps → Jobs → (your job) → **Executions**
  - or `az containerapp job execution list ...`

![](/result.png)

