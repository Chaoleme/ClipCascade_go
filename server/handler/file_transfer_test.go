package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func TestRegisterDeferredFileAndList(t *testing.T) {
	db := newTestDB(t)
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        t.TempDir(),
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	app := fiber.New()
	app.Post("/api/files/deferred/register", withUser("alice", h.RegisterDeferred))
	app.Get("/api/files/deferred", withUser("alice", h.ListDeferred))

	body, _ := json.Marshal(map[string]any{
		"file_name": "movie.mkv",
		"file_size": constants.DefaultFileAutoRelayMaxBytes + 1,
		"mime_type": "video/x-matroska",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/files/deferred/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected register status: %d", resp.StatusCode)
	}

	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response failed: %v", err)
	}
	if created["status"] != model.DeferredFileStatusPending {
		t.Fatalf("unexpected register status payload: %v", created["status"])
	}
	if created["deferred_id"] == "" {
		t.Fatalf("expected deferred_id in response")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/files/deferred", nil)
	listResp, err := app.Test(listReq, -1)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected list status: %d", listResp.StatusCode)
	}

	var items []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&items); err != nil {
		t.Fatalf("decode list response failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 deferred item, got %d", len(items))
	}
	if items[0]["file_name"] != "movie.mkv" {
		t.Fatalf("unexpected file name: %v", items[0]["file_name"])
	}
}

func TestProgressAndCompleteDeferredFile(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	record := &model.DeferredFile{
		DeferredID:    "deferred-1",
		OwnerUsername: "alice",
		FileName:      "archive.zip",
		FileSize:      constants.DefaultFileAutoRelayMaxBytes + 1024,
		MimeType:      "application/zip",
		Status:        model.DeferredFileStatusPending,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	filePath := filepath.Join(tempDir, "relay-file.zip")
	if err := os.WriteFile(filePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("write relay file failed: %v", err)
	}
	relay := &model.FileTransfer{
		RelayID:       "relay-1",
		OwnerUsername: "alice",
		FileName:      "archive.zip",
		FileSize:      5,
		SHA256:        "abc123",
		MimeType:      "application/zip",
		StoragePath:   filePath,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(relay).Error; err != nil {
		t.Fatalf("create relay record failed: %v", err)
	}

	app := fiber.New()
	app.Post("/api/files/deferred/:deferredID/progress", withUser("alice", h.ProgressDeferred))
	app.Post("/api/files/deferred/:deferredID/complete", withUser("alice", h.CompleteDeferred))
	app.Get("/api/files/deferred/:deferredID", withUser("alice", h.GetDeferred))

	progressBody, _ := json.Marshal(map[string]int64{"uploaded_bytes": 3})
	progressReq := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-1/progress", bytes.NewReader(progressBody))
	progressReq.Header.Set("Content-Type", "application/json")
	progressResp, err := app.Test(progressReq, -1)
	if err != nil {
		t.Fatalf("progress request failed: %v", err)
	}
	defer progressResp.Body.Close()
	if progressResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected progress status: %d", progressResp.StatusCode)
	}

	var progressPayload map[string]any
	if err := json.NewDecoder(progressResp.Body).Decode(&progressPayload); err != nil {
		t.Fatalf("decode progress payload failed: %v", err)
	}
	if progressPayload["status"] != model.DeferredFileStatusUploading {
		t.Fatalf("unexpected progress status payload: %v", progressPayload["status"])
	}
	if progressPayload["upload_percent"] != float64(0) {
		t.Fatalf("unexpected upload percent for partial upload: %v", progressPayload["upload_percent"])
	}
	if progressPayload["upload_speed_human"] == "" {
		t.Fatalf("expected upload speed to be exposed during upload")
	}

	completeBody, _ := json.Marshal(map[string]string{"relay_id": "relay-1"})
	completeReq := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-1/complete", bytes.NewReader(completeBody))
	completeReq.Header.Set("Content-Type", "application/json")
	completeResp, err := app.Test(completeReq, -1)
	if err != nil {
		t.Fatalf("complete request failed: %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected complete status: %d", completeResp.StatusCode)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/files/deferred/deferred-1", nil)
	getResp, err := app.Test(getReq, -1)
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get status: %d", getResp.StatusCode)
	}

	var finalRecord map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&finalRecord); err != nil {
		t.Fatalf("decode final record failed: %v", err)
	}
	if finalRecord["status"] != model.DeferredFileStatusReady {
		t.Fatalf("unexpected final status: %v", finalRecord["status"])
	}
	if finalRecord["download_url"] != "/api/files/relay-1" {
		t.Fatalf("unexpected download url: %v", finalRecord["download_url"])
	}
	if finalRecord["upload_percent"] != float64(100) {
		t.Fatalf("unexpected final upload percent: %v", finalRecord["upload_percent"])
	}
}

func TestRequestDeferredWithoutOnlineSourceReturnsPendingMessage(t *testing.T) {
	db := newTestDB(t)
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        t.TempDir(),
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	record := &model.DeferredFile{
		DeferredID:    "deferred-offline",
		OwnerUsername: "alice",
		FileName:      "offline.bin",
		FileSize:      constants.DefaultFileAutoRelayMaxBytes + 1024,
		MimeType:      "application/octet-stream",
		Status:        model.DeferredFileStatusPending,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	app := fiber.New()
	app.Post("/api/files/deferred/:deferredID/request", withUser("alice", h.RequestDeferred))

	req := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-offline/request", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected request status: %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload failed: %v", err)
	}
	if payload["active_clients"] != float64(0) {
		t.Fatalf("expected active_clients=0, got %v", payload["active_clients"])
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected item payload")
	}
	if item["status"] != model.DeferredFileStatusPending {
		t.Fatalf("unexpected item status: %v", item["status"])
	}
	if item["last_error"] == "" {
		t.Fatalf("expected last_error when source client is offline")
	}
}

func TestDeleteDeferredFileRemovesRelayAndRecord(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	relayPath := filepath.Join(tempDir, "relay-delete.bin")
	if err := os.WriteFile(relayPath, []byte("delete-me"), 0o644); err != nil {
		t.Fatalf("write relay file failed: %v", err)
	}
	relay := &model.FileTransfer{
		RelayID:       "relay-delete",
		OwnerUsername: "alice",
		FileName:      "delete.bin",
		FileSize:      int64(len("delete-me")),
		SHA256:        "deadbeef",
		MimeType:      "application/octet-stream",
		StoragePath:   relayPath,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(relay).Error; err != nil {
		t.Fatalf("create relay record failed: %v", err)
	}
	record := &model.DeferredFile{
		DeferredID:    "deferred-delete",
		OwnerUsername: "alice",
		FileName:      "delete.bin",
		FileSize:      relay.FileSize,
		MimeType:      relay.MimeType,
		Status:        model.DeferredFileStatusReady,
		RelayID:       relay.RelayID,
		SHA256:        relay.SHA256,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	app := fiber.New()
	app.Delete("/api/files/deferred/:deferredID", withUser("alice", h.DeleteDeferred))

	req := httptest.NewRequest(http.MethodDelete, "/api/files/deferred/deferred-delete", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("delete request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected delete status: %d", resp.StatusCode)
	}

	if _, err := os.Stat(relayPath); !os.IsNotExist(err) {
		t.Fatalf("expected relay file to be removed, stat err=%v", err)
	}
	if err := db.Where("deferred_id = ?", "deferred-delete").First(&model.DeferredFile{}).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("expected deferred record deleted, got err=%v", err)
	}
	if err := db.Where("relay_id = ?", "relay-delete").First(&model.FileTransfer{}).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("expected relay record deleted, got err=%v", err)
	}
}

func withUser(username string, next fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals("username", username)
		return next(c)
	}
}
