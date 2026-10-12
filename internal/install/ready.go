package install

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// waitReady polls the API's unauthenticated health endpoint until it
// answers. The API starts listening only after the gateway and builder are
// up, so an answer means the service started.
func waitReady(ctx context.Context, apiURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	var last error
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/healthz", nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("the API answered HTTP %d", resp.StatusCode)
		}
		last = err
		select {
		case <-ctx.Done():
			return last
		case <-time.After(time.Second):
		}
	}
}
