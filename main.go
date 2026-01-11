package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// API version constants
const (
	subscriptionAPI = "2022-12-01"
	vmAPI           = "2025-04-01"
	azureResource   = "https://management.azure.com/.default"
)

// SubscriptionListResponse represents the Azure subscriptions API response
type SubscriptionListResponse struct {
	Value []struct {
		SubscriptionID string `json:"subscriptionId"`
	} `json:"value"`
}

// VirtualMachineListResponse represents the Azure VMs API response
type VirtualMachineListResponse struct {
	Value []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ResourceGroup string // will be set from parsing
	} `json:"value"`
}

// getAzureAccessToken obtains a Bearer token using azidentity (managed identity/environment/interactive)
func getAzureAccessToken(ctx context.Context) (string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create credential: %w", err)
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{azureResource},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}
	return token.Token, nil
}

// sendRequest sends HTTP requests with Bearer token
func sendRequest(ctx context.Context, method, url, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

// parseResourceGroup extracts the resource group from a resource ID
// Example resource ID: /subscriptions/{sid}/resourceGroups/{rg}/providers/...
func parseResourceGroup(resourceID string) string {
	rgMarker := "/resourceGroups/"
	rgIdx := strings.Index(resourceID, rgMarker)
	if rgIdx == -1 {
		return ""
	}
	sub := resourceID[rgIdx+len(rgMarker):]
	endIdx := strings.Index(sub, "/")
	if endIdx == -1 {
		return sub
	}
	return sub[:endIdx]
}

func main() {
	ctx := context.Background()

	token, err := getAzureAccessToken(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERR]: Failed to get Azure token: %v\n", err)
		os.Exit(1)
	}

	subscriptionURL := fmt.Sprintf("https://management.azure.com/subscriptions?api-version=%s", subscriptionAPI)
	resp, err := sendRequest(ctx, http.MethodGet, subscriptionURL, token, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERR]: Failed to fetch subscriptions: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "[ERR]: Unexpected status for subscriptions: %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var subsResp SubscriptionListResponse
	if err := json.NewDecoder(resp.Body).Decode(&subsResp); err != nil {
		fmt.Fprintf(os.Stderr, "[ERR]: Failed to parse subscriptions JSON: %v\n", err)
		os.Exit(1)
	}

	for _, sub := range subsResp.Value {
		subscriptionID := sub.SubscriptionID
		fmt.Printf("[INF]: Processing subscription %s\n", subscriptionID)

		vmURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.Compute/virtualMachines?api-version=%s",
			subscriptionID, vmAPI)
		vmResp, err := sendRequest(ctx, http.MethodGet, vmURL, token, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERR]: Failed to fetch VMs for %s: %v\n", subscriptionID, err)
			continue
		}
		defer vmResp.Body.Close()

		if vmResp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "[ERR]: Unexpected status for VMs: %d\n", vmResp.StatusCode)
			continue
		}

		var vms VirtualMachineListResponse
		if err := json.NewDecoder(vmResp.Body).Decode(&vms); err != nil {
			fmt.Fprintf(os.Stderr, "[ERR]: Failed to parse VMs JSON: %v\n", err)
			continue
		}

		for i := range vms.Value {
			vms.Value[i].ResourceGroup = parseResourceGroup(vms.Value[i].ID)
		}

		for _, vm := range vms.Value {
			startURL := fmt.Sprintf(
				"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/start?api-version=%s",
				subscriptionID, vm.ResourceGroup, vm.Name, vmAPI)

			fmt.Printf(
				"[DBG]: Sending %s request to start VM.\n    SubscriptionID: %s\n    ResourceGroup: %s\n    VM Name: %s\n    URL: %s\n",
				http.MethodPost, subscriptionID, vm.ResourceGroup, vm.Name, startURL,
			)

			startResp, err := sendRequest(ctx, http.MethodPost, startURL, token, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ERR]: Failed to start VM %s: %v\n", vm.Name, err)
				continue
			}
			defer startResp.Body.Close()
			if startResp.StatusCode != http.StatusAccepted {
				fmt.Fprintf(os.Stderr,
					"[ERR]: Unexpected status for starting VM %s: %d\n    SubscriptionID: %s\n    ResourceGroup: %s\n    VM Name: %s\n    URL: %s\n",
					vm.Name, startResp.StatusCode, subscriptionID, vm.ResourceGroup, vm.Name, startURL,
				)
				continue
			}
			fmt.Printf("[INF]: VM %s start request accepted\n", vm.Name)
		}
	}
}
