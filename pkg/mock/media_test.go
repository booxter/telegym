package mock

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testUpload struct {
	Filename string
	Bytes    []byte
}

func assertDebugFile(t *testing.T, srv *Server, fileID string, want []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/files/"+fileID, nil)
	srv.Handler().ServeHTTP(rec, req)
	got, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read captured file %s: %v", fileID, err)
	}
	if rec.Code != http.StatusOK || !bytes.Equal(got, want) {
		t.Fatalf("captured file %s status=%d bytes=%q, want %q", fileID, rec.Code, got, want)
	}
}

func newMultipartBody(t *testing.T, fields map[string]string, files map[string]testUpload) (*bytes.Buffer, string) {
	t.Helper()
	body := new(bytes.Buffer)
	w := multipart.NewWriter(body)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field %s: %v", name, err)
		}
	}
	for name, file := range files {
		part, err := w.CreateFormFile(name, file.Filename)
		if err != nil {
			t.Fatalf("create multipart file %s: %v", name, err)
		}
		if _, err := part.Write(file.Bytes); err != nil {
			t.Fatalf("write multipart file %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}
	return body, w.FormDataContentType()
}

// TestSendDocumentRoundtrip covers upload capture, a shape-valid Document
// response, and retrieval of the same bytes through the debug API.
func TestSendDocumentRoundtrip(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "321:document"
	wantBytes := []byte("document payload")
	body, contentType := newMultipartBody(t,
		map[string]string{"chat_id": "42", "caption": "attachment"},
		map[string]testUpload{"document": {Filename: "sample.bin", Bytes: wantBytes}},
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendDocument", body)
	req.Header.Set("Content-Type", contentType)
	srv.Handler().ServeHTTP(rec, req)

	var resp struct {
		OK     bool    `json:"ok"`
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode sendDocument: %v -- body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK || resp.Result.Document == nil {
		t.Fatalf("sendDocument status=%d response=%+v", rec.Code, resp)
	}
	if resp.Result.Document.FileName != "sample.bin" || resp.Result.Document.FileSize != int64(len(wantBytes)) {
		t.Fatalf("unexpected document metadata: %+v", resp.Result.Document)
	}

	assertDebugFile(t, srv, resp.Result.Document.FileID, wantBytes)
}

// TestFileRetentionLimitAndDelete verifies configured capacity failures are
// explicit and captured files can be released through the HTTP debug API.
func TestFileRetentionLimitAndDelete(t *testing.T) {
	limited := New(Config{Quiet: true, FileStoreMaxBytes: 3})
	body, contentType := newMultipartBody(t,
		map[string]string{"chat_id": "42"},
		map[string]testUpload{"document": {Filename: "large.bin", Bytes: []byte("1234")}},
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot1:limit/sendDocument", body)
	req.Header.Set("Content-Type", contentType)
	limited.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized store upload status=%d body=%s, want 413", rec.Code, rec.Body.String())
	}

	srv := New(Config{Quiet: true})
	body, contentType = newMultipartBody(t,
		map[string]string{"chat_id": "42"},
		map[string]testUpload{"document": {Filename: "small.bin", Bytes: []byte("ok")}},
	)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/bot2:delete/sendDocument", body)
	req.Header.Set("Content-Type", contentType)
	srv.Handler().ServeHTTP(rec, req)
	var upload struct {
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upload); err != nil || upload.Result.Document == nil {
		t.Fatalf("decode retained document: err=%v body=%s", err, rec.Body.String())
	}

	fileID := upload.Result.Document.FileID
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/debug/files/"+fileID, nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete captured file status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/debug/files/"+fileID, nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted file GET status=%d, want 404", rec.Code)
	}
}

// TestDeleteAllFiles verifies the collection cleanup endpoint releases all
// captured bytes, including files whose IDs a test runner did not retain.
func TestDeleteAllFiles(t *testing.T) {
	srv := New(Config{Quiet: true})
	first := srv.store.Files.Put("photo", "image/jpeg", "one.jpg", []byte("one"))
	second := srv.store.Files.Put("video", "video/mp4", "two.mp4", []byte("two-two"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/debug/files", nil)
	srv.Handler().ServeHTTP(rec, req)

	var response struct {
		OK           bool  `json:"ok"`
		Deleted      int   `json:"deleted"`
		DeletedBytes int64 `json:"deleted_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode delete-all response: %v -- body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !response.OK || response.Deleted != 2 || response.DeletedBytes != 10 {
		t.Fatalf("delete-all status=%d response=%+v", rec.Code, response)
	}
	for _, fileID := range []string{first, second} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/debug/files/"+fileID, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cleared file %q GET status=%d, want 404", fileID, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/debug/files", nil)
	srv.Handler().ServeHTTP(rec, req)
	response = struct {
		OK           bool  `json:"ok"`
		Deleted      int   `json:"deleted"`
		DeletedBytes int64 `json:"deleted_bytes"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode empty delete-all response: %v -- body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !response.OK || response.Deleted != 0 || response.DeletedBytes != 0 {
		t.Fatalf("empty delete-all status=%d response=%+v", rec.Code, response)
	}
}

// TestSendMediaGroupRoundtrip verifies attach references are resolved by part
// name, response order is preserved, and every album message is recorded.
func TestSendMediaGroupRoundtrip(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "654:album"
	media := `[{"type":"photo","media":"attach://photo_part","caption":"album"},` +
		`{"type":"video","media":"attach://video_part","width":320,"height":180,"duration":2}]`
	body, contentType := newMultipartBody(t,
		map[string]string{"chat_id": "99", "media": media},
		map[string]testUpload{
			"photo_part": {Filename: "one.jpg", Bytes: []byte("photo")},
			"video_part": {Filename: "two.mp4", Bytes: []byte("video")},
		},
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMediaGroup", body)
	req.Header.Set("Content-Type", contentType)
	srv.Handler().ServeHTTP(rec, req)

	var resp struct {
		OK     bool      `json:"ok"`
		Result []Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode sendMediaGroup: %v -- body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK || len(resp.Result) != 2 {
		t.Fatalf("sendMediaGroup status=%d response=%+v", rec.Code, resp)
	}
	if len(resp.Result[0].Photo) == 0 || resp.Result[1].Video == nil {
		t.Fatalf("media group response lost media types: %+v", resp.Result)
	}
	if resp.Result[0].MediaGroupID == "" || resp.Result[0].MediaGroupID != resp.Result[1].MediaGroupID {
		t.Fatalf("media group ids differ: %q and %q", resp.Result[0].MediaGroupID, resp.Result[1].MediaGroupID)
	}
	if got := srv.Store().Bot(token).Messages(99, 10); len(got) != 2 {
		t.Fatalf("stored album messages = %d, want 2", len(got))
	}
	assertDebugFile(t, srv, resp.Result[0].Photo[0].FileID, []byte("photo"))
	assertDebugFile(t, srv, resp.Result[1].Video.FileID, []byte("video"))
}

// TestSendMediaGroupKeepsCachedFileIDs covers Lolek's warm-cache path, where
// no multipart body is present and Telegram file_ids must round-trip unchanged.
func TestSendMediaGroupKeepsCachedFileIDs(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "987:cached_album"
	body := bytes.NewBufferString(`{"chat_id":7,"media":[` +
		`{"type":"photo","media":"cached-photo"},` +
		`{"type":"video","media":"cached-video"}]}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/sendMediaGroup", body)
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	var resp struct {
		Result []Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cached sendMediaGroup: %v", err)
	}
	if rec.Code != http.StatusOK || len(resp.Result) != 2 {
		t.Fatalf("cached sendMediaGroup status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resp.Result[0].Photo[0].FileID != "cached-photo" || resp.Result[1].Video.FileID != "cached-video" {
		t.Fatalf("cached file ids changed: %+v", resp.Result)
	}
}

// TestEditMessageCaptionStored ensures caption edits are visible to debug and
// xk6 consumers as a new event for the original Telegram message id.
func TestEditMessageCaptionStored(t *testing.T) {
	srv := New(Config{Quiet: true})
	const token = "741:caption"
	body := bytes.NewBufferString(`{"chat_id":42,"message_id":12,"caption":"updated"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bot"+token+"/editMessageCaption", body)
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	var resp struct {
		Result Message `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode editMessageCaption: %v", err)
	}
	if rec.Code != http.StatusOK || resp.Result.MessageID != 12 || resp.Result.Caption != "updated" {
		t.Fatalf("editMessageCaption status=%d response=%+v", rec.Code, resp)
	}
	stored := srv.Store().Bot(token).Messages(42, 10)
	if len(stored) != 1 || stored[0].MessageID != 12 || stored[0].Caption != "updated" {
		t.Fatalf("stored caption edit = %+v", stored)
	}
}
