package signalling

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestSegmentFileName(t *testing.T) {
	cases := []struct {
		index int
		want  string
	}{
		{0, "segment_000000.webm"},
		{1, "segment_000001.webm"},
		{42, "segment_000042.webm"},
		{999999, "segment_999999.webm"},
		{proctoringLegacySegment, "full.webm"},
	}
	for _, c := range cases {
		if got := segmentFileName(c.index); got != c.want {
			t.Errorf("segmentFileName(%d) = %q, want %q", c.index, got, c.want)
		}
	}
}

func TestUpsertSegment_NewSegment(t *testing.T) {
	var st proctoringChunkState
	upsertSegment(&st, 0, 5, 5, 1024, 1_000)
	if len(st.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(st.Segments))
	}
	want := proctoringSegmentRow{Index: 0, StartSeq: 5, EndSeq: 5, Bytes: 1024, Chunks: 1, FirstChunkMs: 1000, LastChunkMs: 1000}
	if !reflect.DeepEqual(st.Segments[0], want) {
		t.Errorf("got %+v, want %+v", st.Segments[0], want)
	}
}

func TestUpsertSegment_AppendToLast(t *testing.T) {
	var st proctoringChunkState
	upsertSegment(&st, 0, 5, 5, 1024, 1_000)
	upsertSegment(&st, 0, 6, 6, 2048, 2_000)
	upsertSegment(&st, 0, 7, 7, 512, 3_000)

	if len(st.Segments) != 1 {
		t.Fatalf("expected still 1 segment after appends, got %d", len(st.Segments))
	}
	got := st.Segments[0]
	if got.Chunks != 3 {
		t.Errorf("Chunks = %d, want 3", got.Chunks)
	}
	if got.Bytes != 1024+2048+512 {
		t.Errorf("Bytes = %d, want %d", got.Bytes, 1024+2048+512)
	}
	if got.StartSeq != 5 || got.EndSeq != 7 {
		t.Errorf("seq range = [%d,%d], want [5,7]", got.StartSeq, got.EndSeq)
	}
	if got.FirstChunkMs != 1_000 || got.LastChunkMs != 3_000 {
		t.Errorf("timestamps = [%d,%d], want [1000,3000]", got.FirstChunkMs, got.LastChunkMs)
	}
}

// Straggler chunk: client moved to segment 2 but a late retry for segment 1
// arrives. The row for segment 1 must be updated in place rather than a
// duplicate appended.
func TestUpsertSegment_StragglerUpdatesMiddleRow(t *testing.T) {
	var st proctoringChunkState
	upsertSegment(&st, 0, 0, 0, 100, 1_000)
	upsertSegment(&st, 1, 1, 1, 200, 2_000)
	upsertSegment(&st, 2, 2, 2, 300, 3_000)
	// Now a straggler chunk lands for segment 1.
	upsertSegment(&st, 1, 99, 99, 50, 9_000)

	if len(st.Segments) != 3 {
		t.Fatalf("expected still 3 segments, got %d", len(st.Segments))
	}
	// Segment 1's row must be updated, not duplicated.
	var seg1 *proctoringSegmentRow
	var indices []int
	for i := range st.Segments {
		indices = append(indices, st.Segments[i].Index)
		if st.Segments[i].Index == 1 {
			seg1 = &st.Segments[i]
		}
	}
	if seg1 == nil {
		t.Fatalf("no row for segment 1; indices=%v", indices)
	}
	if seg1.Chunks != 2 {
		t.Errorf("segment 1 Chunks = %d, want 2", seg1.Chunks)
	}
	if seg1.Bytes != 250 {
		t.Errorf("segment 1 Bytes = %d, want 250", seg1.Bytes)
	}
	if seg1.EndSeq != 99 {
		t.Errorf("segment 1 EndSeq = %d, want 99", seg1.EndSeq)
	}
	if seg1.LastChunkMs != 9_000 {
		t.Errorf("segment 1 LastChunkMs = %d, want 9000", seg1.LastChunkMs)
	}
	if !sort.IntsAreSorted(indices) {
		t.Errorf("segment indices are not in ascending order: %v", indices)
	}
}

func TestListSegmentFiles_FiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	// Mix of segment files, the legacy full.webm, state.json and a stray file.
	for _, name := range []string{
		"segment_000002.webm",
		"segment_000000.webm",
		"segment_000010.webm",
		"segment_000001.webm",
		"full.webm",
		"state.json",
		"concat_list.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := listSegmentFiles(dir)
	want := []string{
		"segment_000000.webm",
		"segment_000001.webm",
		"segment_000002.webm",
		"segment_000010.webm",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListSegmentFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if got := listSegmentFiles(dir); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestLoadProctoringState_LegacyMissing(t *testing.T) {
	dir := t.TempDir()
	st, err := loadProctoringState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("loadProctoringState on missing file: %v", err)
	}
	if st.CommittedSeq != -1 {
		t.Errorf("CommittedSeq = %d, want -1 for new state", st.CommittedSeq)
	}
	if len(st.Segments) != 0 {
		t.Errorf("Segments = %d, want 0", len(st.Segments))
	}
}

func TestLoadSaveProctoringState_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	original := proctoringChunkState{
		CommittedSeq:   42,
		TotalBytes:     4096,
		CurrentSegment: 2,
		Segments: []proctoringSegmentRow{
			{Index: 0, StartSeq: 0, EndSeq: 9, Bytes: 1024, Chunks: 10, FirstChunkMs: 1, LastChunkMs: 10},
			{Index: 1, StartSeq: 10, EndSeq: 19, Bytes: 1024, Chunks: 10, FirstChunkMs: 11, LastChunkMs: 20},
			{Index: 2, StartSeq: 20, EndSeq: 42, Bytes: 2048, Chunks: 23, FirstChunkMs: 21, LastChunkMs: 42},
		},
	}
	if err := saveProctoringState(stateFile, original); err != nil {
		t.Fatalf("saveProctoringState: %v", err)
	}
	loaded, err := loadProctoringState(stateFile)
	if err != nil {
		t.Fatalf("loadProctoringState: %v", err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Errorf("roundtrip mismatch\n got  %+v\n want %+v", loaded, original)
	}
}

// Legacy state.json (pre-segments) must still load without panic and default
// to a sensible empty state.
func TestLoadProctoringState_LegacyFileFormat(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	legacy := []byte(`{"committedSeq":5,"totalBytes":1234}`)
	if err := os.WriteFile(stateFile, legacy, 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	st, err := loadProctoringState(stateFile)
	if err != nil {
		t.Fatalf("loadProctoringState on legacy file: %v", err)
	}
	if st.CommittedSeq != 5 {
		t.Errorf("CommittedSeq = %d, want 5", st.CommittedSeq)
	}
	if st.TotalBytes != 1234 {
		t.Errorf("TotalBytes = %d, want 1234", st.TotalBytes)
	}
	if len(st.Segments) != 0 {
		t.Errorf("Segments should be empty for legacy format, got %d", len(st.Segments))
	}
}
