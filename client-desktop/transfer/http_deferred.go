package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func RegisterDeferredFile(client *http.Client, serverURL string, reqBody *DeferredRegisterRequest) (*DeferredFileRecord, error) {
	if reqBody == nil {
		return nil, fmt.Errorf("register request is nil")
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(serverURL, "/")+"/api/files/deferred/register", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register deferred file failed: %s", string(data))
	}
	var record DeferredFileRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func MarkDeferredFileReady(client *http.Client, serverURL string, deferredID string, relayID string) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/complete", map[string]string{"relay_id": relayID})
}

func MarkDeferredFileUploading(client *http.Client, serverURL string, deferredID string, uploadedBytes int64) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/progress", map[string]int64{"uploaded_bytes": uploadedBytes})
}

func MarkDeferredFileFailed(client *http.Client, serverURL string, deferredID string, errMsg string) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/fail", map[string]string{"error": errMsg})
}

func postDeferredStatus(client *http.Client, serverURL string, deferredID string, suffix string, payload any) (*DeferredFileRecord, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(serverURL, "/") + "/api/files/deferred/" + deferredID + suffix
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("update deferred file failed: %s", string(data))
	}
	var record DeferredFileRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return &record, nil
}
