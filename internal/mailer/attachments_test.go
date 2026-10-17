package mailer

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMessageWithAttachments(t *testing.T) {
	from, to := &mail.Address{Address: "a@example.com"}, &mail.Address{Address: "b@example.com"}
	data := bytes.Repeat([]byte("%PDF-1.7 binary\x00\xff"), 40)
	raw, err := buildMessage(from, to, nil, "s", "<p>h</p>", "plain", "example.com",
		[]Attachment{{Name: "عرض سعر.pdf", ContentType: "application/pdf", Data: data}, {Name: "x\r\nBcc: e@x.test", ContentType: "bad\r\ntype", Data: []byte("t")}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mt != "multipart/mixed" {
		t.Fatalf("content type %q", mt)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	alt, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if amt, _, _ := mime.ParseMediaType(alt.Header.Get("Content-Type")); amt != "multipart/alternative" {
		t.Fatalf("first part %q", amt)
	}
	altBody, _ := io.ReadAll(alt)
	if !strings.Contains(string(altBody), "plain") {
		t.Fatalf("alternative body: %s", altBody)
	}
	var names []string
	for i := 0; ; i++ {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.FileName())
		enc, _ := io.ReadAll(p)
		for _, line := range strings.Split(strings.TrimSpace(string(enc)), "\r\n") {
			if len(line) > 76 {
				t.Fatalf("base64 line of %d chars", len(line))
			}
		}
		got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(enc), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && !bytes.Equal(got, data) {
			t.Fatal("attachment bytes changed")
		}
		if i == 1 && p.Header.Get("Content-Type") != `application/octet-stream; name="x Bcc: e@x.test"` {
			t.Fatalf("unsafe content type kept: %q", p.Header.Get("Content-Type"))
		}
	}
	if len(names) != 2 || names[0] != "عرض سعر.pdf" {
		t.Fatalf("file names %q", names)
	}
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatal("header injection through the file name")
	}

	plain, err := buildMessage(from, to, nil, "s", "<p>h</p>", "plain", "example.com", nil)
	if err != nil || !strings.Contains(string(plain), "multipart/alternative") || strings.Contains(string(plain), "multipart/mixed") {
		t.Fatalf("no attachments must stay multipart/alternative: %v", err)
	}
}
