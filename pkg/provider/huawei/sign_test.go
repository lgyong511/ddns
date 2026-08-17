package huawei

import (
	"net/http"
	"strings"
	"testing"
)

func TestSignMatchesOfficialSDKHeaderSelection(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://dns.myhuaweicloud.com/v2/zones?name=example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderXDateTime, "20260817T030405Z")
	signedHeaders := SignedHeaders(request)
	if got := strings.Join(signedHeaders, ";"); got != "content-type;x-sdk-date" {
		t.Fatalf("SignedHeaders() = %q", got)
	}
	huawei := NewHuawei("access-key", "secret")
	if err := huawei.sign(request); err != nil {
		t.Fatal(err)
	}

	if authorization := request.Header.Get(HeaderXAuthorization); !strings.Contains(authorization, "SignedHeaders=content-type;x-sdk-date") {
		t.Fatalf("Authorization = %q", authorization)
	}
}

func TestCanonicalHeadersUsesRequestHostOverride(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://dns.myhuaweicloud.com/v2/zones", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "proxy.example.com"
	if got := CanonicalHeaders(request, []string{HeaderXHost}); got != "host:proxy.example.com\n" {
		t.Fatalf("CanonicalHeaders() = %q", got)
	}
}
