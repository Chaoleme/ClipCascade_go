package model

import "time"

const (
	DeferredFileStatusPending   = "pending"
	DeferredFileStatusRequested = "requested"
	DeferredFileStatusUploading = "uploading"
	DeferredFileStatusReady     = "ready"
	DeferredFileStatusFailed    = "failed"
)

// DeferredFile 保存大文件懒加载下载所需的元信息。
type DeferredFile struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	DeferredID    string    `gorm:"uniqueIndex;size:64;not null" json:"deferred_id"`
	OwnerUsername string    `gorm:"index;size:50;not null" json:"owner_username"`
	FileName      string    `gorm:"size:255;not null" json:"file_name"`
	FileSize      int64     `gorm:"not null" json:"file_size"`
	MimeType      string    `gorm:"size:255" json:"mime_type"`
	Status        string    `gorm:"index;size:20;not null" json:"status"`
	RelayID       string    `gorm:"size:64" json:"relay_id"`
	SHA256        string    `gorm:"size:64" json:"sha256"`
	LastError     string    `gorm:"size:255" json:"last_error"`
	UploadBytes   int64     `gorm:"not null;default:0" json:"upload_bytes"`
	RequestedAt   time.Time `json:"requested_at"`
	UploadStarted time.Time `json:"upload_started"`
	UploadUpdated time.Time `json:"upload_updated"`
	ReadyAt       time.Time `json:"ready_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `gorm:"index" json:"expires_at"`
}
