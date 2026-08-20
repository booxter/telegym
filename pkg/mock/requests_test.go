package mock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRequestJournalCapturesExplicitAndGenericMethods(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "901:request_journal_token"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/banChatMember", bytes.NewBufferString(`{"chat_id":-42,"user_id":7}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("banChatMember status=%d body=%s", rec.Code, rec.Body.String())
	}

	form := url.Values{"chat_id": {"-42"}, "message_id": {"123"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/bot"+token+"/deleteMessage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleteMessage status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/requests/"+token, nil)
	srv.Handler().ServeHTTP(rec, req)
	var response struct {
		Count    int          `json:"count"`
		Requests []APIRequest `json:"requests"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode request journal: %v", err)
	}
	if response.Count != 2 {
		t.Fatalf("request count=%d requests=%+v", response.Count, response.Requests)
	}
	ban, deleted := response.Requests[0], response.Requests[1]
	if ban.Method != "banChatMember" || ban.Status != http.StatusOK ||
		toInt64(ban.Parameters["chat_id"]) != -42 || toInt64(ban.Parameters["user_id"]) != 7 {
		t.Fatalf("unexpected ban request: %+v", ban)
	}
	if deleted.Method != "deleteMessage" || deleted.Status != http.StatusOK ||
		toInt64(deleted.Parameters["chat_id"]) != -42 || toInt(deleted.Parameters["message_id"]) != 123 ||
		deleted.Seq <= ban.Seq {
		t.Fatalf("unexpected delete request: %+v after %+v", deleted, ban)
	}
}

func TestRequestJournalFiltersAndClears(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "902:request_filter_token"

	for _, method := range []string{"getMe", "deleteMessage", "getMe"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/"+method, nil)
		srv.Handler().ServeHTTP(rec, req)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/requests/"+token+"?method=getMe&limit=1", nil)
	srv.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"count":1`) || !strings.Contains(rec.Body.String(), `"method":"getMe"`) {
		t.Fatalf("unexpected filtered journal: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/debug/requests/"+token+"/clear", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cleared":3`) {
		t.Fatalf("unexpected clear response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
