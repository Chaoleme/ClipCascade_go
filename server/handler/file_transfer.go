package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/pkg/protocol"
	"github.com/clipcascade/pkg/sizefmt"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
)

// FileTransferHandler 提供基于 HTTP 的文件中转上传/下载能力。
type FileTransferHandler struct {
	DB     *gorm.DB
	Config *config.Config
	Hub    *WSHub
}

type deferredRegisterRequest struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type"`
}

type deferredCompleteRequest struct {
	RelayID string `json:"relay_id"`
}

type deferredProgressRequest struct {
	UploadedBytes int64 `json:"uploaded_bytes"`
}

type deferredFailRequest struct {
	Error string `json:"error"`
}

type deferredRequestPayload struct {
	DeferredID string `json:"deferred_id"`
}

type deferredFileResponse struct {
	DeferredID       string     `json:"deferred_id"`
	FileName         string     `json:"file_name"`
	FileSize         int64      `json:"file_size"`
	FileSizeHuman    string     `json:"file_size_human"`
	MimeType         string     `json:"mime_type,omitempty"`
	Status           string     `json:"status"`
	StatusDetail     string     `json:"status_detail,omitempty"`
	RelayID          string     `json:"relay_id,omitempty"`
	SHA256           string     `json:"sha256,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	UploadBytes      int64      `json:"upload_bytes"`
	UploadPercent    int        `json:"upload_percent"`
	UploadSpeedBPS   int64      `json:"upload_speed_bps"`
	UploadSpeedHuman string     `json:"upload_speed_human,omitempty"`
	DownloadURL      string     `json:"download_url,omitempty"`
	RequestedAt      *time.Time `json:"requested_at,omitempty"`
	UploadStarted    *time.Time `json:"upload_started,omitempty"`
	UploadUpdated    *time.Time `json:"upload_updated,omitempty"`
	ReadyAt          *time.Time `json:"ready_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
}

func NewFileTransferHandler(db *gorm.DB, cfg *config.Config, hub *WSHub) *FileTransferHandler {
	return &FileTransferHandler{DB: db, Config: cfg, Hub: hub}
}

func (h *FileTransferHandler) Upload(c *fiber.Ctx) error {
	if !h.Config.FileRelayEnabled {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "file relay disabled"})
	}

	// Early size rejection using optional hint header sent by the client.
	if sizeHint, err := strconv.ParseInt(string(c.Request().Header.Peek("X-File-Size")), 10, 64); err == nil {
		if sizeHint <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid file size"})
		}
		if h.Config.FileRelayMaxBytes > 0 && sizeHint > h.Config.FileRelayMaxBytes {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "file too large", "max_bytes": h.Config.FileRelayMaxBytes})
		}
	}

	// Parse multipart boundary from Content-Type without buffering the body.
	_, params, err := mime.ParseMediaType(string(c.Request().Header.ContentType()))
	if err != nil || params["boundary"] == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid content type"})
	}

	mr := multipart.NewReader(c.Request().BodyStream(), params["boundary"])
	var filePart *multipart.Part
	var mimeType string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "failed to parse multipart"})
		}
		if p.FormName() == "file" {
			filePart = p
			mimeType = p.Header.Get("Content-Type")
			break
		}
		_ = p.Close()
	}
	if filePart == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing file"})
	}
	defer filePart.Close()

	name := sanitizeUploadFilename(filePart.FileName())
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid filename"})
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "application/octet-stream"
	}

	if err := os.MkdirAll(h.Config.FileRelayDir, 0o755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create relay dir"})
	}

	username, _ := c.Locals("username").(string)
	relayID, err := randomID(16)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create relay id"})
	}

	tempPath := filepath.Join(h.Config.FileRelayDir, relayID+".part")
	finalPath := filepath.Join(h.Config.FileRelayDir, relayID+filepath.Ext(name))
	defer os.Remove(tempPath)

	out, err := os.Create(tempPath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create relay file"})
	}

	// Stream directly to disk — no full-body buffering.
	var src io.Reader = filePart
	if h.Config.FileRelayMaxBytes > 0 {
		src = io.LimitReader(filePart, h.Config.FileRelayMaxBytes+1)
	}
	hasher := sha256.New()
	written, copyErr := copyWithHash(out, src, hasher)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to store file"})
	}
	if written == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid file size"})
	}
	if h.Config.FileRelayMaxBytes > 0 && written > h.Config.FileRelayMaxBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "file too large", "max_bytes": h.Config.FileRelayMaxBytes})
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to finalize upload"})
	}

	record := &model.FileTransfer{
		RelayID:       relayID,
		OwnerUsername: username,
		FileName:      name,
		FileSize:      written,
		SHA256:        hex.EncodeToString(hasher.Sum(nil)),
		MimeType:      mimeType,
		StoragePath:   finalPath,
		ExpiresAt:     time.Now().Add(time.Duration(h.Config.FileRelayTTLSeconds) * time.Second),
	}
	if err := h.DB.Create(record).Error; err != nil {
		_ = os.Remove(finalPath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to persist relay metadata"})
	}

	return c.JSON(fiber.Map{
		"relay_id":  record.RelayID,
		"file_name": record.FileName,
		"file_size": record.FileSize,
		"sha256":    record.SHA256,
		"mime_type": record.MimeType,
	})
}

func (h *FileTransferHandler) Download(c *fiber.Ctx) error {
	if !h.Config.FileRelayEnabled {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "file relay disabled"})
	}

	relayID := strings.TrimSpace(c.Params("relayID"))
	if relayID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing relay id"})
	}

	var record model.FileTransfer
	if err := h.DB.Where("relay_id = ?", relayID).First(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load relay metadata"})
	}
	if !record.ExpiresAt.IsZero() && time.Now().After(record.ExpiresAt) {
		_ = os.Remove(record.StoragePath)
		_ = h.DB.Delete(&record).Error
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "file expired"})
	}

	if _, err := os.Stat(record.StoragePath); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "file missing"})
	}

	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", record.FileName))
	if record.MimeType != "" {
		c.Set(fiber.HeaderContentType, record.MimeType)
		c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	}
	c.Set(fiber.HeaderContentLength, fmt.Sprintf("%d", record.FileSize))
	return c.SendFile(record.StoragePath)
}

func (h *FileTransferHandler) RegisterDeferred(c *fiber.Ctx) error {
	if !h.Config.FileRelayEnabled {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{"error": "file relay disabled"})
	}

	var body deferredRegisterRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}

	body.FileName = sanitizeUploadFilename(body.FileName)
	if body.FileName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid filename"})
	}
	if body.FileSize <= constants.DefaultFileAutoRelayMaxBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file should use direct or auto relay flow"})
	}
	if h.Config.FileRelayMaxBytes > 0 && body.FileSize > h.Config.FileRelayMaxBytes {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "file too large", "max_bytes": h.Config.FileRelayMaxBytes})
	}
	if strings.TrimSpace(body.MimeType) == "" {
		body.MimeType = "application/octet-stream"
	}

	deferredID, err := randomID(16)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create deferred id"})
	}

	record := &model.DeferredFile{
		DeferredID:    deferredID,
		OwnerUsername: currentUsername(c),
		FileName:      body.FileName,
		FileSize:      body.FileSize,
		MimeType:      strings.TrimSpace(body.MimeType),
		Status:        model.DeferredFileStatusPending,
		UploadBytes:   0,
		ExpiresAt:     time.Now().Add(time.Duration(h.Config.FileRelayTTLSeconds) * time.Second),
	}
	if err := h.DB.Create(record).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to store deferred file"})
	}

	return c.JSON(h.buildDeferredResponse(record))
}

func (h *FileTransferHandler) ListDeferred(c *fiber.Ctx) error {
	username := currentUsername(c)
	var records []model.DeferredFile
	if err := h.DB.Where("owner_username = ? AND expires_at > ?", username, time.Now()).
		Order("created_at desc").
		Limit(50).
		Find(&records).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred files"})
	}

	resp := make([]deferredFileResponse, 0, len(records))
	for i := range records {
		if err := h.normalizeDeferredRecord(&records[i]); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to normalize deferred files"})
		}
		resp = append(resp, h.buildDeferredResponse(&records[i]))
	}
	return c.JSON(resp)
}

func (h *FileTransferHandler) GetDeferred(c *fiber.Ctx) error {
	record, err := h.loadOwnedDeferred(currentUsername(c), c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}
	if record.ExpiresAt.Before(time.Now()) {
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "deferred file expired"})
	}
	if err := h.normalizeDeferredRecord(&record); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to normalize deferred file"})
	}
	return c.JSON(h.buildDeferredResponse(&record))
}

func (h *FileTransferHandler) DeleteDeferred(c *fiber.Ctx) error {
	username := currentUsername(c)
	record, err := h.loadOwnedDeferred(username, c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}

	if strings.TrimSpace(record.RelayID) != "" {
		if err := h.deleteRelayFile(username, record.RelayID); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete relay file"})
		}
	}
	if err := h.DB.Delete(&record).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete deferred file"})
	}

	return c.JSON(fiber.Map{
		"message":     "deferred file deleted",
		"deferred_id": record.DeferredID,
	})
}

func (h *FileTransferHandler) RequestDeferred(c *fiber.Ctx) error {
	record, err := h.loadOwnedDeferred(currentUsername(c), c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}
	if record.ExpiresAt.Before(time.Now()) {
		return c.Status(fiber.StatusGone).JSON(fiber.Map{"error": "deferred file expired"})
	}
	if err := h.normalizeDeferredRecord(&record); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to normalize deferred file"})
	}
	if record.Status == model.DeferredFileStatusReady && record.RelayID != "" {
		return c.JSON(h.buildDeferredResponse(&record))
	}
	if record.Status == model.DeferredFileStatusRequested || record.Status == model.DeferredFileStatusUploading {
		return c.JSON(fiber.Map{
			"item":           h.buildDeferredResponse(&record),
			"active_clients": 1,
		})
	}

	now := time.Now()
	updates := map[string]any{
		"status":         model.DeferredFileStatusRequested,
		"requested_at":   now,
		"last_error":     "",
		"upload_bytes":   int64(0),
		"upload_started": time.Time{},
		"upload_updated": time.Time{},
	}
	if err := h.DB.Model(&record).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update deferred file"})
	}
	record.Status = model.DeferredFileStatusRequested
	record.RequestedAt = now
	record.LastError = ""
	record.UploadBytes = 0
	record.UploadStarted = time.Time{}
	record.UploadUpdated = time.Time{}

	activeClients, err := h.pushDeferredRequest(record.OwnerUsername, record.DeferredID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to notify desktop client"})
	}
	if activeClients == 0 {
		errMsg := "no online source desktop client is available"
		revert := map[string]any{
			"status":         model.DeferredFileStatusPending,
			"last_error":     errMsg,
			"upload_bytes":   int64(0),
			"upload_started": time.Time{},
			"upload_updated": time.Time{},
		}
		if err := h.DB.Model(&record).Updates(revert).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update deferred file"})
		}
		record.Status = model.DeferredFileStatusPending
		record.LastError = errMsg
		record.UploadBytes = 0
		record.UploadStarted = time.Time{}
		record.UploadUpdated = time.Time{}
		return c.JSON(fiber.Map{
			"item":           h.buildDeferredResponse(&record),
			"active_clients": 0,
		})
	}

	return c.JSON(fiber.Map{
		"item":           h.buildDeferredResponse(&record),
		"active_clients": activeClients,
	})
}

func (h *FileTransferHandler) ProgressDeferred(c *fiber.Ctx) error {
	record, err := h.loadOwnedDeferred(currentUsername(c), c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}

	var body deferredProgressRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	if body.UploadedBytes < 0 {
		body.UploadedBytes = 0
	}
	if record.FileSize > 0 && body.UploadedBytes > record.FileSize {
		body.UploadedBytes = record.FileSize
	}

	now := time.Now()
	startedAt := record.UploadStarted
	if startedAt.IsZero() {
		startedAt = now
	}
	updates := map[string]any{
		"status":         model.DeferredFileStatusUploading,
		"upload_bytes":   body.UploadedBytes,
		"upload_started": startedAt,
		"upload_updated": now,
		"last_error":     "",
	}
	if err := h.DB.Model(&record).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update deferred progress"})
	}
	record.Status = model.DeferredFileStatusUploading
	record.UploadBytes = body.UploadedBytes
	record.UploadStarted = startedAt
	record.UploadUpdated = now
	record.LastError = ""

	return c.JSON(h.buildDeferredResponse(&record))
}

func (h *FileTransferHandler) CompleteDeferred(c *fiber.Ctx) error {
	username := currentUsername(c)
	record, err := h.loadOwnedDeferred(username, c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}

	var body deferredCompleteRequest
	if err := c.BodyParser(&body); err != nil || strings.TrimSpace(body.RelayID) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "relay_id required"})
	}

	var relay model.FileTransfer
	if err := h.DB.Where("relay_id = ? AND owner_username = ?", strings.TrimSpace(body.RelayID), username).First(&relay).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "relay file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load relay file"})
	}

	now := time.Now()
	updates := map[string]any{
		"status":         model.DeferredFileStatusReady,
		"relay_id":       relay.RelayID,
		"sha256":         relay.SHA256,
		"mime_type":      relay.MimeType,
		"file_size":      relay.FileSize,
		"file_name":      relay.FileName,
		"upload_bytes":   relay.FileSize,
		"upload_updated": now,
		"ready_at":       now,
		"expires_at":     relay.ExpiresAt,
		"last_error":     "",
	}
	if err := h.DB.Model(&record).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to finalize deferred file"})
	}
	record.Status = model.DeferredFileStatusReady
	record.RelayID = relay.RelayID
	record.SHA256 = relay.SHA256
	record.MimeType = relay.MimeType
	record.FileSize = relay.FileSize
	record.FileName = relay.FileName
	record.UploadBytes = relay.FileSize
	record.UploadUpdated = now
	record.ReadyAt = now
	record.ExpiresAt = relay.ExpiresAt
	record.LastError = ""

	return c.JSON(h.buildDeferredResponse(&record))
}

func (h *FileTransferHandler) FailDeferred(c *fiber.Ctx) error {
	record, err := h.loadOwnedDeferred(currentUsername(c), c.Params("deferredID"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "deferred file not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load deferred file"})
	}

	var body deferredFailRequest
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
	}
	errMsg := strings.TrimSpace(body.Error)
	if errMsg == "" {
		errMsg = "source client could not upload the file"
	}
	updates := map[string]any{
		"status":         model.DeferredFileStatusFailed,
		"relay_id":       "",
		"sha256":         "",
		"last_error":     errMsg,
		"upload_bytes":   int64(0),
		"upload_started": time.Time{},
		"upload_updated": time.Time{},
		"ready_at":       time.Time{},
	}
	if err := h.DB.Model(&record).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update deferred file"})
	}
	record.Status = model.DeferredFileStatusFailed
	record.RelayID = ""
	record.SHA256 = ""
	record.LastError = errMsg
	record.UploadBytes = 0
	record.UploadStarted = time.Time{}
	record.UploadUpdated = time.Time{}
	record.ReadyAt = time.Time{}

	return c.JSON(h.buildDeferredResponse(&record))
}

func (h *FileTransferHandler) CleanupExpired() error {
	var expired []model.FileTransfer
	if err := h.DB.Where("expires_at > ? AND expires_at <= ?", time.Time{}, time.Now()).Find(&expired).Error; err != nil {
		return err
	}
	for _, record := range expired {
		_ = os.Remove(record.StoragePath)
		_ = h.DB.Delete(&record).Error
	}

	var expiredDeferred []model.DeferredFile
	if err := h.DB.Where("expires_at > ? AND expires_at <= ?", time.Time{}, time.Now()).Find(&expiredDeferred).Error; err != nil {
		return err
	}
	for _, record := range expiredDeferred {
		_ = h.DB.Delete(&record).Error
	}
	return nil
}

func (h *FileTransferHandler) RunCleanupLoop(interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = h.CleanupExpired()
		}
	}
}

func (h *FileTransferHandler) pushDeferredRequest(username string, deferredID string) (int, error) {
	payload, err := json.Marshal(deferredRequestPayload{DeferredID: deferredID})
	if err != nil {
		return 0, err
	}
	clipData := &protocol.ClipboardData{
		Type:    constants.TypeFileRequest,
		Payload: string(payload),
	}
	bodyBytes, err := clipData.Encode()
	if err != nil {
		return 0, err
	}
	msgFrame := protocol.MessageFrame(constants.SubscriptionDestination, "server-0", "", string(bodyBytes))
	if h.Hub == nil {
		return 0, nil
	}
	return h.Hub.SendToUser(username, msgFrame.Encode()), nil
}

func (h *FileTransferHandler) loadOwnedDeferred(username, deferredID string) (model.DeferredFile, error) {
	var record model.DeferredFile
	err := h.DB.Where("owner_username = ? AND deferred_id = ?", username, strings.TrimSpace(deferredID)).First(&record).Error
	return record, err
}

func (h *FileTransferHandler) normalizeDeferredRecord(record *model.DeferredFile) error {
	if strings.TrimSpace(record.RelayID) == "" {
		return nil
	}
	relay, ok, err := h.loadRelay(record.RelayID)
	if err != nil {
		return err
	}
	if !ok {
		updates := map[string]any{
			"status":         model.DeferredFileStatusPending,
			"relay_id":       "",
			"sha256":         "",
			"upload_bytes":   int64(0),
			"upload_started": time.Time{},
			"upload_updated": time.Time{},
			"ready_at":       time.Time{},
			"last_error":     "",
		}
		if err := h.DB.Model(record).Updates(updates).Error; err != nil {
			return err
		}
		record.Status = model.DeferredFileStatusPending
		record.RelayID = ""
		record.SHA256 = ""
		record.UploadBytes = 0
		record.UploadStarted = time.Time{}
		record.UploadUpdated = time.Time{}
		record.ReadyAt = time.Time{}
		record.LastError = ""
		return nil
	}
	if record.Status != model.DeferredFileStatusReady || record.SHA256 != relay.SHA256 || record.FileSize != relay.FileSize || record.MimeType != relay.MimeType {
		updates := map[string]any{
			"status":       model.DeferredFileStatusReady,
			"sha256":       relay.SHA256,
			"mime_type":    relay.MimeType,
			"file_size":    relay.FileSize,
			"file_name":    relay.FileName,
			"upload_bytes": relay.FileSize,
			"expires_at":   relay.ExpiresAt,
			"last_error":   "",
		}
		if err := h.DB.Model(record).Updates(updates).Error; err != nil {
			return err
		}
		record.Status = model.DeferredFileStatusReady
		record.SHA256 = relay.SHA256
		record.MimeType = relay.MimeType
		record.FileSize = relay.FileSize
		record.FileName = relay.FileName
		record.UploadBytes = relay.FileSize
		record.ExpiresAt = relay.ExpiresAt
		record.LastError = ""
	}
	return nil
}

func (h *FileTransferHandler) loadRelay(relayID string) (*model.FileTransfer, bool, error) {
	var relay model.FileTransfer
	if err := h.DB.Where("relay_id = ?", relayID).First(&relay).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !relay.ExpiresAt.IsZero() && time.Now().After(relay.ExpiresAt) {
		return &relay, false, nil
	}
	if _, err := os.Stat(relay.StoragePath); err != nil {
		return &relay, false, nil
	}
	return &relay, true, nil
}

func (h *FileTransferHandler) deleteRelayFile(username string, relayID string) error {
	var relay model.FileTransfer
	if err := h.DB.Where("relay_id = ? AND owner_username = ?", strings.TrimSpace(relayID), username).First(&relay).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if relay.StoragePath != "" {
		if err := os.Remove(relay.StoragePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return h.DB.Delete(&relay).Error
}

func (h *FileTransferHandler) buildDeferredResponse(record *model.DeferredFile) deferredFileResponse {
	return deferredFileResponse{
		DeferredID:       record.DeferredID,
		FileName:         record.FileName,
		FileSize:         record.FileSize,
		FileSizeHuman:    sizefmt.FormatBytes(record.FileSize),
		MimeType:         record.MimeType,
		Status:           record.Status,
		StatusDetail:     deferredStatusDetail(record),
		RelayID:          record.RelayID,
		SHA256:           record.SHA256,
		LastError:        record.LastError,
		UploadBytes:      record.UploadBytes,
		UploadPercent:    progressPercent(record.UploadBytes, record.FileSize),
		UploadSpeedBPS:   uploadSpeedBytesPerSec(record),
		UploadSpeedHuman: uploadSpeedHuman(record),
		DownloadURL:      h.downloadURL(record),
		RequestedAt:      optionalTime(record.RequestedAt),
		UploadStarted:    optionalTime(record.UploadStarted),
		UploadUpdated:    optionalTime(record.UploadUpdated),
		ReadyAt:          optionalTime(record.ReadyAt),
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
		ExpiresAt:        record.ExpiresAt,
	}
}

func (h *FileTransferHandler) downloadURL(record *model.DeferredFile) string {
	if record.Status != model.DeferredFileStatusReady || strings.TrimSpace(record.RelayID) == "" {
		return ""
	}
	return "/api/files/" + record.RelayID
}

func currentUsername(c *fiber.Ctx) string {
	username, _ := c.Locals("username").(string)
	return username
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func progressPercent(done, total int64) int {
	if total <= 0 || done <= 0 {
		return 0
	}
	if done >= total {
		return 100
	}
	return int((done * 100) / total)
}

func deferredStatusDetail(record *model.DeferredFile) string {
	switch record.Status {
	case model.DeferredFileStatusPending:
		if strings.TrimSpace(record.LastError) != "" {
			return record.LastError
		}
		return "Waiting for someone to request this file"
	case model.DeferredFileStatusRequested:
		return "Source desktop notified, waiting for upload to start"
	case model.DeferredFileStatusUploading:
		speed := uploadSpeedHuman(record)
		if speed != "" {
			return fmt.Sprintf("Uploading %s of %s (%d%%) at %s", sizefmt.FormatBytes(record.UploadBytes), sizefmt.FormatBytes(record.FileSize), progressPercent(record.UploadBytes, record.FileSize), speed)
		}
		return fmt.Sprintf("Uploading %s of %s (%d%%)", sizefmt.FormatBytes(record.UploadBytes), sizefmt.FormatBytes(record.FileSize), progressPercent(record.UploadBytes, record.FileSize))
	case model.DeferredFileStatusReady:
		return "Ready to download"
	case model.DeferredFileStatusFailed:
		if strings.TrimSpace(record.LastError) != "" {
			return record.LastError
		}
		return "Upload failed"
	default:
		return ""
	}
}

func uploadSpeedBytesPerSec(record *model.DeferredFile) int64 {
	if record == nil || record.UploadBytes <= 0 || record.UploadStarted.IsZero() || record.UploadUpdated.IsZero() {
		return 0
	}
	elapsed := record.UploadUpdated.Sub(record.UploadStarted)
	if elapsed <= 0 {
		elapsed = time.Second
	}
	speed := int64(float64(record.UploadBytes) / elapsed.Seconds())
	if speed < 0 {
		return 0
	}
	return speed
}

func uploadSpeedHuman(record *model.DeferredFile) string {
	speed := uploadSpeedBytesPerSec(record)
	if speed <= 0 {
		return ""
	}
	return sizefmt.FormatBytes(speed) + "/s"
}

func sanitizeUploadFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "" || name == "." || name == string(os.PathSeparator) {
		return ""
	}
	return name
}

func randomID(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func copyWithHash(dst *os.File, src io.Reader, hasher hashWriter) (int64, error) {
	return io.CopyBuffer(io.MultiWriter(dst, hasher), src, make([]byte, 256*1024))
}

type hashWriter interface {
	Write(p []byte) (n int, err error)
	Sum(b []byte) []byte
}
