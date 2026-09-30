package mock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type richResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		MessageID      int          `json:"message_id"`
		ReplyToMessage *Message     `json:"reply_to_message"`
		ReplyMarkup    *ReplyMarkup `json:"reply_markup"`
		RichMessage    struct {
			Blocks []map[string]any `json:"blocks"`
		} `json:"rich_message"`
	} `json:"result"`
}

func postRichJSON(t *testing.T, srv *Server, token string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendRichMessage", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeRich(t *testing.T, rec *httptest.ResponseRecorder) richResponse {
	t.Helper()
	var resp richResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode sendRichMessage: %v -- body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK {
		t.Fatalf("sendRichMessage status=%d body=%s", rec.Code, rec.Body.String())
	}
	return resp
}

func mediaFileID(t *testing.T, block map[string]any, field string) string {
	t.Helper()
	switch media := block[field].(type) {
	case []any:
		sizes, _ := media[len(media)-1].(map[string]any)
		return toString(sizes["file_id"])
	case map[string]any:
		return toString(media["file_id"])
	}
	t.Fatalf("block has no %s file: %+v", field, block)
	return ""
}

// TestSendRichMessageMultipartCollage covers attach:// uploads nested in a
// collage next to a cached file_id, plus reply and inline keyboard.
func TestSendRichMessageMultipartCollage(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "321:rich"
	richMessage := `{"blocks":[
		{"type":"collage","blocks":[
			{"type":"photo","photo":{"type":"photo","media":"attach://first"}},
			{"type":"video","video":{"type":"video","media":"attach://second","width":640,"height":360,"duration":7}},
			{"type":"photo","photo":{"type":"photo","media":"CACHED_PHOTO"}}
		]},
		{"type":"footer","text":["requested by ",{"type":"text_mention","text":"Ann","user":{"id":7,"is_bot":false,"first_name":"Ann"}}]}
	]}`
	body, contentType := newMultipartBody(t,
		map[string]string{
			"chat_id":          "42",
			"rich_message":     richMessage,
			"reply_parameters": `{"message_id":5,"allow_sending_without_reply":true}`,
			"reply_markup":     `{"inline_keyboard":[[{"text":"Original","url":"https://example.com/p/1"}]]}`,
		},
		map[string]testUpload{
			"first":  {Filename: "a.jpg", Bytes: []byte("photo bytes")},
			"second": {Filename: "b.mp4", Bytes: []byte("video bytes")},
		},
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendRichMessage", body)
	req.Header.Set("Content-Type", contentType)
	srv.Handler().ServeHTTP(rec, req)
	resp := decodeRich(t, rec)

	blocks := resp.Result.RichMessage.Blocks
	if len(blocks) != 2 || blocks[0]["type"] != "collage" || blocks[1]["type"] != "footer" {
		t.Fatalf("unexpected blocks: %+v", blocks)
	}
	collage, _ := blocks[0]["blocks"].([]any)
	if len(collage) != 3 {
		t.Fatalf("collage items = %d, want 3", len(collage))
	}
	photo, _ := collage[0].(map[string]any)
	video, _ := collage[1].(map[string]any)
	cached, _ := collage[2].(map[string]any)
	assertDebugFile(t, srv, mediaFileID(t, photo, "photo"), []byte("photo bytes"))
	assertDebugFile(t, srv, mediaFileID(t, video, "video"), []byte("video bytes"))
	if got := mediaFileID(t, cached, "photo"); got != "CACHED_PHOTO" {
		t.Fatalf("cached photo file_id = %q", got)
	}
	if width := video["video"].(map[string]any)["width"]; width != float64(640) {
		t.Fatalf("video width = %v, want 640", width)
	}

	if resp.Result.ReplyToMessage == nil || resp.Result.ReplyToMessage.MessageID != 5 {
		t.Fatalf("reply_to_message = %+v, want message 5", resp.Result.ReplyToMessage)
	}
	markup := resp.Result.ReplyMarkup
	if markup == nil || len(markup.InlineKeyboard) != 1 || markup.InlineKeyboard[0][0].URL != "https://example.com/p/1" {
		t.Fatalf("reply_markup = %+v", markup)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/messages/"+token+"?chat_id=42", nil))
	if !strings.Contains(rec.Body.String(), `"rich_message"`) {
		t.Fatalf("rich message missing from debug messages: %s", rec.Body.String())
	}
}

// TestSendRichMessageHTMLMediaList covers html content: files from the
// media list are captured, blocks come back empty.
func TestSendRichMessageHTMLMediaList(t *testing.T) {
	srv := New(Config{Quiet: true})
	body, contentType := newMultipartBody(t,
		map[string]string{
			"chat_id":      "42",
			"rich_message": `{"html":"<img src=\"tg://photo?id=p\"/>","media":[{"id":"p","media":{"type":"photo","media":"attach://p"}}]}`,
		},
		map[string]testUpload{"p": {Filename: "p.jpg", Bytes: []byte("listed")}},
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot1:html/sendRichMessage", body)
	req.Header.Set("Content-Type", contentType)
	srv.Handler().ServeHTTP(rec, req)
	resp := decodeRich(t, rec)
	if len(resp.Result.RichMessage.Blocks) != 0 {
		t.Fatalf("html message blocks = %+v, want none", resp.Result.RichMessage.Blocks)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/files", nil))
	if !strings.Contains(rec.Body.String(), `"count":1,`) {
		t.Fatalf("media list upload not captured: %s", rec.Body.String())
	}
}

func TestSendRichMessageRejectsInvalidInput(t *testing.T) {
	photo := func(ref string) map[string]any {
		return map[string]any{"type": "photo", "photo": map[string]any{"type": "photo", "media": ref}}
	}
	tooMany := make([]any, 0, maxRichMessageMedia+1)
	for i := 0; i <= maxRichMessageMedia; i++ {
		tooMany = append(tooMany, photo(fmt.Sprintf("ID_%d", i)))
	}

	cases := map[string]map[string]any{
		"no chat":         {"rich_message": map[string]any{"blocks": []any{}}},
		"two formats":     {"chat_id": 1, "rich_message": map[string]any{"blocks": []any{}, "html": "<p>x</p>"}},
		"no format":       {"chat_id": 1, "rich_message": map[string]any{}},
		"missing attach":  {"chat_id": 1, "rich_message": map[string]any{"blocks": []any{photo("attach://gone")}}},
		"empty reference": {"chat_id": 1, "rich_message": map[string]any{"blocks": []any{photo("")}}},
		"too many media":  {"chat_id": 1, "rich_message": map[string]any{"blocks": tooMany}},
		"unknown media type": {"chat_id": 1, "rich_message": map[string]any{
			"html":  "<p>x</p>",
			"media": []any{map[string]any{"id": "x", "media": map[string]any{"type": "sticker", "media": "STICKER_ID"}}},
		}},
	}
	srv := New(Config{Quiet: true})
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postRichJSON(t, srv, "1:bad", payload)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/files", nil))
	if !strings.Contains(rec.Body.String(), `"count":0,`) {
		t.Fatalf("rejected requests stored files: %s", rec.Body.String())
	}
}
