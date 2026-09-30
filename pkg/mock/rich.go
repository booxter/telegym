package mock

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

const maxRichMessageMedia = 50

var richMediaFields = map[string]string{
	"photo":     "photo",
	"video":     "video",
	"animation": "animation",
	"audio":     "audio",
	"document":  "document",
}

type inputRichMessage struct {
	Blocks   []any            `json:"blocks"`
	HTML     string           `json:"html"`
	Markdown string           `json:"markdown"`
	Media    []map[string]any `json:"media"`
}

func parseInputRichMessage(v any) (inputRichMessage, error) {
	raw, err := rawJSON(v)
	if err != nil {
		return inputRichMessage{}, err
	}
	var msg inputRichMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return inputRichMessage{}, err
	}
	formats := 0
	if msg.Blocks != nil {
		formats++
	}
	if msg.HTML != "" {
		formats++
	}
	if msg.Markdown != "" {
		formats++
	}
	if formats != 1 {
		return inputRichMessage{}, fmt.Errorf("exactly one of blocks, html or markdown must be used")
	}
	return msg, nil
}

type richMediaResolver struct {
	h     *Handlers
	c     *gin.Context
	count int
}

func (r *richMediaResolver) resolve(v any) (any, error) {
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			resolved, err := r.resolve(item)
			if err != nil {
				return nil, err
			}
			x[i] = resolved
		}
	case map[string]any:
		for key, value := range x {
			if kind, ok := richMediaFields[key]; ok {
				if media, ok := value.(map[string]any); ok {
					file, err := r.media(kind, media)
					if err != nil {
						return nil, err
					}
					x[key] = file
					continue
				}
			}
			resolved, err := r.resolve(value)
			if err != nil {
				return nil, err
			}
			x[key] = resolved
		}
	}
	return v, nil
}

// media checks the kind before resolveInputFile, which stores uploads.
func (r *richMediaResolver) media(kind string, media map[string]any) (any, error) {
	if _, ok := richMediaFields[kind]; !ok {
		return nil, fmt.Errorf("unsupported rich message media type %q", kind)
	}
	r.count++
	if r.count > maxRichMessageMedia {
		return nil, fmt.Errorf("rich message can contain at most %d media", maxRichMessageMedia)
	}
	reference := toString(media["media"])
	if reference == "" {
		return nil, fmt.Errorf("media reference is required")
	}
	file, err := r.h.resolveInputFile(r.c, kind, reference, "")
	if err != nil {
		return nil, err
	}
	var m Message
	if err := applyMedia(&m, kind, file, media); err != nil {
		return nil, err
	}
	switch kind {
	case "photo":
		return m.Photo, nil
	case "video":
		return m.Video, nil
	case "animation":
		return m.Animation, nil
	case "audio":
		return m.Audio, nil
	default:
		return m.Document, nil
	}
}

func (h *Handlers) SendRichMessage(c *gin.Context, bot *botEntry) {
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
	input, err := parseInputRichMessage(body["rich_message"])
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: invalid rich_message: "+err.Error())
		return
	}

	resolver := &richMediaResolver{h: h, c: c}
	for _, item := range input.Media {
		media, ok := item["media"].(map[string]any)
		if !ok {
			errResp(c, http.StatusBadRequest, "Bad Request: rich_message media item requires media")
			return
		}
		if _, err := resolver.media(toString(media["type"]), media); err != nil {
			respondInputFileError(c, err)
			return
		}
	}
	if _, err := resolver.resolve(input.Blocks); err != nil {
		respondInputFileError(c, err)
		return
	}

	m := h.newBotMessage(bot, chatID)
	m.RichMessage = &RichMessage{Blocks: input.Blocks}
	if input.Blocks == nil {
		m.RichMessage.Blocks = []any{}
	}
	m.ReplyMarkup = parseReplyMarkup(body["reply_markup"])
	if replyTo := parseReplyToMessageID(body["reply_parameters"]); replyTo != 0 {
		m.ReplyToMessage = &Message{MessageID: replyTo, Chat: m.Chat, Date: m.Date}
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}
