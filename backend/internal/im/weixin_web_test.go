package im

import (
	"testing"
)

func TestWeixinRedirectAndLoginXML(t *testing.T) {
	target, err := weixinRedirect("https://wx.qq.com/cgi-bin/mmwebwx-bin/webwxnewloginpage?ticket=abc&fun=old&version=v1")
	if err != nil {
		t.Fatal(err)
	}
	if target.RawQuery != "ticket=abc&fun=new&version=v2" {
		t.Fatalf("redirect query mismatch: %s", target.RawQuery)
	}
	if _, err := weixinRedirect("https://evil.example/cgi-bin/mmwebwx-bin/webwxnewloginpage"); err == nil {
		t.Fatal("untrusted redirect must fail")
	}
	session, err := NewWeixinWebSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	login, err := session.parseLogin(`<error><ret>0</ret><message></message><skey>skey</skey><wxsid>sid</wxsid><wxuin>12345</wxuin><pass_ticket>pass</pass_ticket></error>`, "wx.qq.com")
	if err != nil {
		t.Fatal(err)
	}
	if login.WxUin != "12345" || login.WxSid != "sid" || login.SKey != "skey" {
		t.Fatalf("login xml mismatch: %+v", login)
	}
	if _, err := session.parseLogin(`<!DOCTYPE x><error><ret>0</ret></error>`, "wx.qq.com"); err == nil {
		t.Fatal("doctype must fail")
	}
}

func TestWeixinWebSessionRoundTrip(t *testing.T) {
	session, err := NewWeixinWebSession(map[string]any{
		"domain":      "wx.qq.com",
		"login":       map[string]any{"wxuin": "12345", "wxsid": "sid", "skey": "skey"},
		"BaseRequest": map[string]any{"Uin": 12345, "Sid": "sid", "Skey": "skey", "DeviceID": "e123456789012345"},
		"user":        "@user",
		"SyncKey":     map[string]any{"Count": 1, "List": []any{map[string]any{"Key": 1, "Val": 2}}},
		"cookies":     []any{map[string]any{"key": "wxuin", "value": "12345", "domain": "wx.qq.com", "path": "/", "secure": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	exported := session.Session()
	if exported["user"] != "@user" {
		t.Fatalf("session mismatch: %+v", exported)
	}
	cookies := exported["cookies"].([]map[string]any)
	if len(cookies) != 1 || cookies[0]["key"] != "wxuin" {
		t.Fatalf("cookie mismatch: %+v", cookies)
	}
	if _, err := NewWeixinWebSession(map[string]any{
		"domain":      "evil.example",
		"login":       map[string]any{"wxuin": "12345", "wxsid": "sid", "skey": "skey"},
		"BaseRequest": map[string]any{"Uin": 12345, "Sid": "sid", "Skey": "skey", "DeviceID": "e123456789012345"},
	}); err == nil {
		t.Fatal("invalid domain must fail")
	}
}
