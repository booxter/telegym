package mock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const maxUploadFileBytes = 50 * 1024 * 1024

var errUploadTooLarge = errors.New("uploaded file exceeds 50 MB")

type resolvedInputFile struct {
	FileID       string
	FileUniqueID string
	Filename     string
	ContentType  string
	Size         int64
}

// resolveInputFile turns a multipart upload, attach:// reference, cached
// file_id, or URL into the descriptor used to build Telegram media objects.
func (h *Handlers) resolveInputFile(c *gin.Context, kind, reference, defaultPart string) (resolvedInputFile, error) {
	partName := defaultPart
	if attached := strings.TrimPrefix(reference, "attach://"); attached != reference {
		if attached == "" {
			return resolvedInputFile{}, fmt.Errorf("empty attach reference")
		}
		partName = attached
	}

	if partName != "" {
		file, found, err := h.captureMultipartFile(c, kind, partName)
		if err != nil {
			return resolvedInputFile{}, err
		}
		if found {
			return file, nil
		}
	}

	if strings.HasPrefix(reference, "attach://") {
		return resolvedInputFile{}, fmt.Errorf("multipart attachment %q not found", partName)
	}
	if reference == "" {
		reference = fakeFileID(kind)
	}
	return resolvedInputFile{FileID: reference, FileUniqueID: reference + "_u"}, nil
}

func (h *Handlers) captureMultipartFile(c *gin.Context, kind, partName string) (resolvedInputFile, bool, error) {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		return resolvedInputFile{}, false, nil
	}
	fileHdr, err := c.FormFile(partName)
	if errors.Is(err, http.ErrMissingFile) {
		return resolvedInputFile{}, false, nil
	}
	if err != nil {
		return resolvedInputFile{}, false, err
	}
	src, err := fileHdr.Open()
	if err != nil {
		return resolvedInputFile{}, false, err
	}
	defer func() { _ = src.Close() }()

	data, err := io.ReadAll(io.LimitReader(src, maxUploadFileBytes+1))
	if err != nil {
		return resolvedInputFile{}, false, err
	}
	if len(data) > maxUploadFileBytes {
		return resolvedInputFile{}, false, errUploadTooLarge
	}
	contentType := fileHdr.Header.Get("Content-Type")
	fileID := h.Store.Files.Put(kind, contentType, fileHdr.Filename, data)
	return resolvedInputFile{
		FileID:       fileID,
		FileUniqueID: fileID + "_u",
		Filename:     fileHdr.Filename,
		ContentType:  contentType,
		Size:         int64(len(data)),
	}, true, nil
}

func respondInputFileError(c *gin.Context, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errUploadTooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	errResp(c, status, "Bad Request: "+err.Error())
}

func applyMedia(m *Message, kind string, file resolvedInputFile, attrs map[string]any) error {
	width := max(1, toInt(attrs["width"]))
	height := max(1, toInt(attrs["height"]))
	duration := max(1, toInt(attrs["duration"]))

	switch kind {
	case "photo":
		m.Photo = []PhotoSize{{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			Width: width, Height: height, FileSize: int(file.Size),
		}}
	case "video":
		m.Video = &Video{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			Width: width, Height: height, Duration: duration,
			FileName: file.Filename, MimeType: file.ContentType, FileSize: file.Size,
		}
	case "animation":
		m.Animation = &Animation{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			Width: width, Height: height, Duration: duration,
			FileName: file.Filename, MimeType: file.ContentType, FileSize: file.Size,
		}
	case "document":
		m.Document = &Document{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			FileName: file.Filename, MimeType: file.ContentType, FileSize: file.Size,
		}
	case "audio":
		m.Audio = &Audio{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			Duration: duration, FileName: file.Filename,
			MimeType: file.ContentType, FileSize: file.Size,
		}
	default:
		return fmt.Errorf("unsupported media type %q", kind)
	}
	return nil
}

type inputMedia struct {
	Type            string   `json:"type"`
	Media           string   `json:"media"`
	Caption         string   `json:"caption"`
	CaptionEntities []Entity `json:"caption_entities"`
	Width           int      `json:"width"`
	Height          int      `json:"height"`
	Duration        int      `json:"duration"`
}

func parseInputMedia(v any) ([]inputMedia, error) {
	var raw []byte
	var err error
	if text, ok := v.(string); ok {
		raw = []byte(text)
	} else {
		raw, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	var media []inputMedia
	if err := json.Unmarshal(raw, &media); err != nil {
		return nil, err
	}
	return media, nil
}

func (h *Handlers) SendMediaGroup(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}
	chatID := toInt64(body["chat_id"])
	if chatID == 0 {
		errResp(c, http.StatusBadRequest, "Bad Request: chat_id is required")
		return
	}
	media, err := parseInputMedia(body["media"])
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: invalid media: "+err.Error())
		return
	}
	if len(media) < 2 || len(media) > 10 {
		errResp(c, http.StatusBadRequest, "Bad Request: media must include 2-10 items")
		return
	}

	groupID := fakeFileID("media_group")
	messages := make([]Message, 0, len(media))
	for _, item := range media {
		if item.Media == "" {
			errResp(c, http.StatusBadRequest, "Bad Request: media reference is required")
			return
		}
		file, err := h.resolveInputFile(c, item.Type, item.Media, "")
		if err != nil {
			respondInputFileError(c, err)
			return
		}
		m := h.newBotMessage(bot, chatID)
		m.MediaGroupID = groupID
		m.Caption = item.Caption
		m.Text = item.Caption
		m.CaptionEntities = item.CaptionEntities
		attrs := map[string]any{
			"width": item.Width, "height": item.Height, "duration": item.Duration,
		}
		if err := applyMedia(&m, item.Type, file, attrs); err != nil {
			errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
			return
		}
		messages = append(messages, m)
	}

	for _, m := range messages {
		bot.AppendMessage(m)
	}
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: messages})
}
