package schools

import "testing"

func TestLoad(t *testing.T) {
	if n := Count(); n < 2000 {
		t.Fatalf("expected the full school list, got %d", n)
	}
	s, ok := Lookup("1399")
	if !ok || s.Name != "珠海科技学院" {
		t.Fatalf("Lookup(1399) = %+v, %v", s, ok)
	}
	if s, ok := Lookup("1949"); !ok || s.Name != `"互联网+"时代智慧校园建设研讨会` {
		t.Fatalf("quoted CSV field parsed wrong: %+v", s)
	}
}

func TestSearch(t *testing.T) {
	res, total := Search("珠海科技", 8)
	if total == 0 || res[0].Code != "1399" {
		t.Fatalf("Search(珠海科技) = %+v (total %d)", res, total)
	}

	res, _ = Search("1399", 8)
	if len(res) == 0 || res[0].Code != "1399" {
		t.Fatalf("Search by code = %+v", res)
	}

	// Multiple keywords must all match.
	res, _ = Search("珠海 科技", 8)
	if len(res) == 0 || res[0].Code != "1399" {
		t.Fatalf("Search(珠海 科技) = %+v", res)
	}

	// Exact name ranks before longer names containing it.
	res, _ = Search("郑州大学", 8)
	if len(res) == 0 || res[0].Name != "郑州大学" {
		t.Fatalf("Search(郑州大学) = %+v", res)
	}

	if res, total := Search("大学", 5); len(res) != 5 || total <= 5 {
		t.Fatalf("limit not applied: %d results, total %d", len(res), total)
	}
	if res, total := Search("   ", 5); res != nil || total != 0 {
		t.Fatal("blank query should match nothing")
	}
	if _, total := Search("不存在的学校名称xyz", 5); total != 0 {
		t.Fatal("unexpected match")
	}
}

func TestIsCode(t *testing.T) {
	for in, want := range map[string]bool{"1399": true, "1000008365": true, "": false, "13a": false, "珠海": false} {
		if IsCode(in) != want {
			t.Errorf("IsCode(%q) != %v", in, want)
		}
	}
}
