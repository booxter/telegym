package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Handlers wires the Bot API method endpoints to the Store and WebhookDispatcher.
// Every method either returns a valid Message (so telego et al. can
// unmarshal) or a boolean - matching the real Telegram API contract.
type Handlers struct {
	Store             *Store
	WebhookDispatcher *WebhookDispatcher
	updates           *updateDispatcher
}

// errResp writes a Telegram-shaped error envelope.
func errResp(c *gin.Context, code int, desc string) {
	c.JSON(code, APIResponse{OK: false, ErrorCode: code, Description: desc})
}

// readBody combines query parameters with parsed JSON, form-encoded, or
// multipart fields. The handler decides which keys it cares about.
func readBody(c *gin.Context) (map[string]any, error) {
	out := make(map[string]any)
	for k, v := range c.Request.URL.Query() {
		if len(v) == 1 {
			out[k] = v[0]
		} else {
			out[k] = v
		}
	}

	ct := c.GetHeader("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			return nil, err
		}
		for k, v := range c.Request.PostForm {
			if len(v) == 1 {
				out[k] = v[0]
			} else {
				out[k] = v
			}
		}
		return out, nil
	}
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		if err := c.Request.ParseForm(); err != nil {
			return nil, err
		}
		for k, v := range c.Request.PostForm {
			if len(v) == 1 {
				out[k] = v[0]
			} else {
				out[k] = v
			}
		}
		return out, nil
	}
	if c.Request.ContentLength == 0 {
		return out, nil
	}
	body, err := c.GetRawData()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return out, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	for k, v := range decoded {
		out[k] = v
	}
	return out, nil
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func toInt(v any) int {
	return int(toInt64(v))
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		value, _ := strconv.ParseBool(x)
		return value
	}
	return false
}

func parseReplyMarkup(v any) *ReplyMarkup {
	if v == nil {
		return nil
	}
	// reply_markup over multipart arrives as JSON string; over JSON as object.
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			return nil
		}
	}
	var m ReplyMarkup
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

// rawJSON returns the JSON of a structured parameter: a JSON string over
// multipart, an already decoded value over a JSON body.
func rawJSON(v any) ([]byte, error) {
	if text, ok := v.(string); ok {
		return []byte(text), nil
	}
	return json.Marshal(v)
}

func parseReplyToMessageID(v any) int {
	if v == nil {
		return 0
	}
	raw, err := rawJSON(v)
	if err != nil {
		return 0
	}
	var params struct {
		MessageID int `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return 0
	}
	return params.MessageID
}

func parseEntities(v any) []Entity {
	if v == nil {
		return nil
	}
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			return nil
		}
	}
	var ents []Entity
	if err := json.Unmarshal(raw, &ents); err != nil {
		return nil
	}
	return ents
}

// newBotMessage builds a baseline Message authored by the given bot at "now".
func (h *Handlers) newBotMessage(bot *botEntry, chatID int64) Message {
	return Message{
		MessageID: h.Store.NextMessageID(),
		From:      &bot.identity,
		Date:      time.Now().Unix(),
		Chat:      Chat{ID: chatID, Type: "private"},
	}
}

// fakeFileID generates a stable-looking file_id for media we never store.
func fakeFileID(kind string) string {
	return fmt.Sprintf("MOCK_%s_%d", strings.ToUpper(kind), time.Now().UnixNano())
}

// =====================================================================
// Bot API endpoints
// =====================================================================

func (h *Handlers) GetUpdates(c *gin.Context, bot *botEntry) {
	if h.WebhookDispatcher != nil && h.WebhookDispatcher.hasTarget(bot) {
		errResp(c, http.StatusConflict, "Conflict: can't use getUpdates method while webhook is active")
		return
	}
	body, err := readBody(c)
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}
	timeout := time.Duration(max(0, toInt(body["timeout"]))) * time.Second
	updates := bot.updates.Poll(c.Request.Context(), toInt64(body["offset"]), toInt(body["limit"]), timeout)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: updates})
}

func (h *Handlers) GetMe(c *gin.Context, bot *botEntry) {
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: bot.identity})
}

func (h *Handlers) SendMessage(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	chatID := toInt64(body["chat_id"])
	if chatID == 0 {
		errResp(c, 400, "Bad Request: chat_id is required")
		return
	}

	m := h.newBotMessage(bot, chatID)
	m.Text = toString(body["text"])
	m.Entities = parseEntities(body["entities"])
	m.ReplyMarkup = parseReplyMarkup(body["reply_markup"])

	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) EditMessageText(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	chatID := toInt64(body["chat_id"])
	m := Message{
		MessageID:   toInt(body["message_id"]),
		From:        &bot.identity,
		Date:        time.Now().Unix(),
		Chat:        Chat{ID: chatID, Type: "private"},
		Text:        toString(body["text"]),
		Entities:    parseEntities(body["entities"]),
		ReplyMarkup: parseReplyMarkup(body["reply_markup"]),
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) EditMessageReplyMarkup(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	m := Message{
		MessageID:   toInt(body["message_id"]),
		From:        &bot.identity,
		Date:        time.Now().Unix(),
		Chat:        Chat{ID: toInt64(body["chat_id"]), Type: "private"},
		ReplyMarkup: parseReplyMarkup(body["reply_markup"]),
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) EditMessageCaption(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}
	if toString(body["inline_message_id"]) != "" {
		c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
		return
	}
	m := Message{
		MessageID:       toInt(body["message_id"]),
		From:            &bot.identity,
		Date:            time.Now().Unix(),
		Chat:            Chat{ID: toInt64(body["chat_id"]), Type: "private"},
		Caption:         toString(body["caption"]),
		CaptionEntities: parseEntities(body["caption_entities"]),
		ReplyMarkup:     parseReplyMarkup(body["reply_markup"]),
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) EditMessageMedia(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	m := Message{
		MessageID:   toInt(body["message_id"]),
		From:        &bot.identity,
		Date:        time.Now().Unix(),
		Chat:        Chat{ID: toInt64(body["chat_id"]), Type: "private"},
		Caption:     toString(body["caption"]),
		ReplyMarkup: parseReplyMarkup(body["reply_markup"]),
		Photo:       []PhotoSize{{FileID: fakeFileID("photo"), FileUniqueID: fakeFileID("photo_u"), Width: 1, Height: 1}},
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) DeleteMessage(c *gin.Context, _ *botEntry) {
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
}

func (h *Handlers) AnswerCallbackQuery(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err == nil {
		text := toString(body["text"])
		if text != "" {
			cbID := toString(body["callback_query_id"])
			showAlert, _ := body["show_alert"].(bool)
			if chatID := bot.LookupCallbackChat(cbID); chatID != 0 {
				bot.PushToast(chatID, Toast{
					Text:      text,
					ShowAlert: showAlert,
					At:        time.Now().Unix(),
				})
			}
		}
	}
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
}

func (h *Handlers) sendMedia(c *gin.Context, bot *botEntry, kind string) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	chatID := toInt64(body["chat_id"])
	if chatID == 0 {
		errResp(c, 400, "Bad Request: chat_id is required")
		return
	}
	m := h.newBotMessage(bot, chatID)
	m.Caption = toString(body["caption"])
	m.Text = m.Caption
	m.CaptionEntities = parseEntities(body["caption_entities"])
	m.ReplyMarkup = parseReplyMarkup(body["reply_markup"])

	file, err := h.resolveInputFile(c, kind, toString(body[kind]), kind)
	if err != nil {
		respondInputFileError(c, err)
		return
	}

	if kind == "sticker" {
		// Real Telegram stores an emoji per sticker in its sticker-set DB
		// and returns it on every send. The mock has no such DB, so we
		// only know an emoji when the bot explicitly passes one in the
		// request. Leave the field empty otherwise - the debug UI will
		// surface the absence so it's obvious nothing was supplied,
		// instead of hiding it behind a default.
		m.Sticker = &Sticker{
			FileID: file.FileID, FileUniqueID: file.FileUniqueID,
			Type: "regular", Width: 1, Height: 1,
			Emoji: toString(body["emoji"]),
		}
	} else if err := applyMedia(&m, kind, file, body); err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) SendPhoto(c *gin.Context, b *botEntry)     { h.sendMedia(c, b, "photo") }
func (h *Handlers) SendVideo(c *gin.Context, b *botEntry)     { h.sendMedia(c, b, "video") }
func (h *Handlers) SendAnimation(c *gin.Context, b *botEntry) { h.sendMedia(c, b, "animation") }
func (h *Handlers) SendSticker(c *gin.Context, b *botEntry)   { h.sendMedia(c, b, "sticker") }
func (h *Handlers) SendDocument(c *gin.Context, b *botEntry)  { h.sendMedia(c, b, "document") }

func (h *Handlers) SendDice(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	chatID := toInt64(body["chat_id"])
	emoji := toString(body["emoji"])
	if emoji == "" {
		emoji = "🎲"
	}
	// Real Telegram returns a random 1..6 (1..64 for slot etc). For load
	// tests, a deterministic-ish value derived from time is fine and avoids
	// pulling in math/rand state contention.
	value := int(time.Now().UnixNano()%6) + 1

	m := h.newBotMessage(bot, chatID)
	m.Dice = &Dice{Emoji: emoji, Value: value}
	bot.AppendMessage(m)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: m})
}

func (h *Handlers) GetChatMember(c *gin.Context, _ *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	userID := toInt64(body["user_id"])
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: ChatMember{
		Status: "member",
		User: User{
			ID:        userID,
			IsBot:     false,
			FirstName: "Mock User",
		},
	}})
}

func (h *Handlers) SetWebhook(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, 400, "Bad Request: "+err.Error())
		return
	}
	url := toString(body["url"])
	secret := toString(body["secret_token"])
	if toBool(body["drop_pending_updates"]) {
		bot.updates.Clear()
	}
	bot.SetWebhook(url, secret)
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
}

func (h *Handlers) DeleteWebhook(c *gin.Context, bot *botEntry) {
	body, err := readBody(c)
	if err != nil {
		errResp(c, http.StatusBadRequest, "Bad Request: "+err.Error())
		return
	}
	if toBool(body["drop_pending_updates"]) {
		bot.updates.Clear()
	}
	bot.SetWebhook("", "")
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
}

func (h *Handlers) GetWebhookInfo(c *gin.Context, bot *botEntry) {
	url, _ := bot.Webhook()
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: WebhookInfo{
		URL:                  url,
		HasCustomCertificate: false,
		PendingUpdateCount:   bot.updates.Len(),
		MaxConnections:       40,
	}})
}

// GenericOK handles methods without a custom implementation but that bots
// commonly call. Always returns {ok:true, result:true} which is the right
// shape for the most common boolean-returning methods (sendChatAction,
// pinChatMessage, promoteChatMember, etc).
func (h *Handlers) GenericOK(c *gin.Context, _ *botEntry) {
	c.JSON(http.StatusOK, APIResponse{OK: true, Result: true})
}
