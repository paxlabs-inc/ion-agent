package memory

import "testing"

func TestTaxonomyHasExactClosedWireCodes(t *testing.T) {
	want := []Type{
		Type("0x01"),
		Type("0x02"),
		Type("0x03"),
		Type("0x04"),
		Type("0x05"),
		Type("0x06"),
		Type("0x07"),
		Type("0x08"),
		Type("0x09"),
	}
	got := Types()
	if len(got) != len(want) {
		t.Fatalf("Types length = %d", len(got))
	}
	for index := range want {
		if got[index] != want[index] || !got[index].Valid() {
			t.Fatalf("Types[%d] = %q", index, got[index])
		}
	}
	got[0] = Type("changed")
	if Types()[0] != Identity {
		t.Fatal("Types exposed mutable taxonomy storage")
	}
	if Type("0x00").Valid() || Type("0x0a").Valid() {
		t.Fatal("taxonomy accepted an out-of-range code")
	}
}
