# VMStarter

## Introduction

![](/logo_small.png)

Azure have built-in VM auto-shutdown feature, that stops a virtual machine at specified time and can optionally send a notification (email or webhook) before the shutdown happens.

There is no automatic start features for VMs in Azure. If you want the VM to come back on by itself at a certain time, you need a separate “start VM on a schedule” solution. This repository have a Golang app named VMStarter, which allows to start VM using Azure REST API calls.

## How it works?

VMStarter is a Go-based worker whose only job is to iterate over every Azure subscription visible to its identity, enumerate all virtual machines, and send POST request for each VM to start it. On top of that you can use an Azure Container Apps (ACA) Job for VM start operations on demand or via schedule without wiring up custom automation per subscription.

Code itself is available [here](/main.go). 

## How to run it?

### Prerequisites

- Azure CLI
- Permission to create resource groups, Container Apps environments, and managed identities in the deployment subscription.
- Docker or another OCI builder to produce the container image.

### Deployment

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

## Required permissions for the app

The managed identity (or workload identity) that runs VMStarter must have:

| Scope | Role | Why |
|-------|------|-----|
| Subscription (each target) | Reader | Allows the job to enumerate the subscription and list VMs via ARM. |
| Subscription (each target) | Virtual Machine Contributor *or* the `Microsoft.Compute/virtualMachines/start/action` custom role | Grants rights to invoke the VM `start` action. |

Remember to scope assignments narrowly. If you only need to start VMs in specific resource groups, grant the roles at the resource-group scope instead of whole subscriptions.
