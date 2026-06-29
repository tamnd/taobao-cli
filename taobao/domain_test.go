package taobao

import (
	"testing"
)

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "taobao" {
		t.Errorf("Scheme = %q, want taobao", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "taobao" {
		t.Errorf("Identity.Binary = %q, want taobao", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in  string
		typ string
		id  string
	}{
		{"123456", "listing", "123456"},
		{"item123", "listing", "item123"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

func TestLocate(t *testing.T) {
	got, err := Domain{}.Locate("listing", "123456")
	want := "https://item.taobao.com/item.htm?id=123456"
	if err != nil || got != want {
		t.Errorf("Locate = (%q, %v), want (%q, nil)", got, err, want)
	}
}

func TestLocate_UnknownType(t *testing.T) {
	_, err := Domain{}.Locate("page", "abc")
	if err == nil {
		t.Error("expected error for unknown resource type")
	}
}

func TestValidSort(t *testing.T) {
	for _, s := range []string{"sale", "price-asc", "price-desc", "new"} {
		if !ValidSort(s) {
			t.Errorf("ValidSort(%q) = false, want true", s)
		}
	}
	if ValidSort("invalid") {
		t.Error("ValidSort(invalid) = true, want false")
	}
}
