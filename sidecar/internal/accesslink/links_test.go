package accesslink

import "testing"

func TestNormalizeLocalLinks(t *testing.T) {
	got, err := Normalize([]Link{{"首页", "http://localhost:3000"}, {"重复", "http://localhost:3000/"}, {"后台", "http://localhost:3000/admin"}})
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, link := range []Link{{"", "http://localhost:3000"}, {"wrong", "javascript:alert(1)"}, {"remote", "https://example.com"}} {
		if _, err := Normalize([]Link{link}); err == nil {
			t.Fatalf("accepted %+v", link)
		}
	}
}
