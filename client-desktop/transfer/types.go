package transfer

import "time"

type Direction string

type Status string

const (
	DirectionUpload   Direction = "upload"
	DirectionDownload Direction = "download"
)

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Task struct {
	ID         string
	Direction  Direction
	FileName   string
	TotalBytes int64
	DoneBytes  int64
	Status     Status
	StartedAt  time.Time
	UpdatedAt  time.Time
	Err        string
	SHA256     string
	RelayID    string
}

type FileOfferPayload struct {
	TransferID string `json:"transfer_id"`
	RelayID    string `json:"relay_id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	SHA256     string `json:"sha256"`
	MimeType   string `json:"mime_type,omitempty"`
	Transport  string `json:"transport"`
}

type UploadResult struct {
	RelayID  string `json:"relay_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	SHA256   string `json:"sha256"`
	MimeType string `json:"mime_type"`
}

type DeferredRegisterRequest struct {
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	MimeType string `json:"mime_type,omitempty"`
}

type DeferredFileRecord struct {
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

type DeferredFileRequest struct {
	DeferredID string `json:"deferred_id"`
}
