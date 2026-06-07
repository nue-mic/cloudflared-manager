package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Meta is the persisted daemon-level metadata stored at /data/meta.json.
// It tracks the user-defined display order. Whether an instance auto-
// starts on daemon boot is now driven by frpsmgr.manualStart inside each
// config file; the legacy AutoStart list is kept only so old meta.json
// files round-trip without losing the key.
type Meta struct {
	Version      int               `json:"version"`
	AutoStart    []string          `json:"auto_start"`
	Sort         []string          `json:"sort"`
	LogViewSince map[string]int64  `json:"log_view_since,omitempty"`
	Names        map[string]string `json:"names,omitempty"`
	Manual       map[string]bool   `json:"manual,omitempty"`
}

func defaultMeta() *Meta {
	return &Meta{
		Version:      1,
		AutoStart:    []string{},
		Sort:         []string{},
		LogViewSince: map[string]int64{},
		Names:        map[string]string{},
		Manual:       map[string]bool{},
	}
}

type metaStore struct {
	path string
	mu   sync.Mutex
	data *Meta
}

func openMetaStore(path string) (*metaStore, error) {
	s := &metaStore{path: path, data: defaultMeta()}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		_ = json.Unmarshal(b, s.data)
		if s.data.Version == 0 {
			s.data.Version = 1
		}
		if s.data.AutoStart == nil {
			s.data.AutoStart = []string{}
		}
		if s.data.Sort == nil {
			s.data.Sort = []string{}
		}
		if s.data.LogViewSince == nil {
			s.data.LogViewSince = map[string]int64{}
		}
		// R5: 旧 meta.json 没有 names/manual，读出来是 nil map，写入会 panic。
		if s.data.Names == nil {
			s.data.Names = map[string]string{}
		}
		if s.data.Manual == nil {
			s.data.Manual = map[string]bool{}
		}
	case errors.Is(err, os.ErrNotExist):
		// fresh install; write a stub so operators can see the file
		if err := s.flushLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

func (s *metaStore) snapshot() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := *s.data
	m.AutoStart = append([]string(nil), s.data.AutoStart...)
	m.Sort = append([]string(nil), s.data.Sort...)
	m.LogViewSince = make(map[string]int64, len(s.data.LogViewSince))
	for k, v := range s.data.LogViewSince {
		m.LogViewSince[k] = v
	}
	// Deep-copy Names/Manual too, otherwise the snapshot shares the live
	// maps and a caller mutating them would corrupt the store.
	m.Names = make(map[string]string, len(s.data.Names))
	for k, v := range s.data.Names {
		m.Names[k] = v
	}
	m.Manual = make(map[string]bool, len(s.data.Manual))
	for k, v := range s.data.Manual {
		m.Manual[k] = v
	}
	return m
}

func (s *metaStore) setSort(order []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sort = append([]string(nil), order...)
	return s.flushLocked()
}

// dropIDs removes id from both AutoStart and Sort. Used after a config
// file is deleted.
func (s *metaStore) dropIDs(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idset := make(map[string]struct{}, len(ids))
	for _, x := range ids {
		idset[x] = struct{}{}
	}
	s.data.AutoStart = filterOut(s.data.AutoStart, idset)
	s.data.Sort = filterOut(s.data.Sort, idset)
	for id := range idset {
		delete(s.data.LogViewSince, id)
		delete(s.data.Names, id)
		delete(s.data.Manual, id)
	}
	return s.flushLocked()
}

// name returns the display name recorded for id ("" if none).
func (s *metaStore) name(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Names[id]
}

// setName records the display name for id.
func (s *metaStore) setName(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Names == nil {
		s.data.Names = map[string]string{}
	}
	s.data.Names[id] = name
	return s.flushLocked()
}

// manualStart reports whether id is marked manual-start (no auto-start on boot).
func (s *metaStore) manualStart(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Manual[id]
}

// setManualStart records the manual-start flag for id.
func (s *metaStore) setManualStart(id string, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Manual == nil {
		s.data.Manual = map[string]bool{}
	}
	s.data.Manual[id] = v
	return s.flushLocked()
}

func (s *metaStore) flushLocked() error {
	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// setLogViewSince 记录"用户在 unixMilli 时刻清空了 id 的日志视图"。
// GET /logs 和 WS /logs/tail 后续会跳过时间戳早于此值的行，达到逻辑清空效果。
func (s *metaStore) setLogViewSince(id string, unixMilli int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.LogViewSince == nil {
		s.data.LogViewSince = map[string]int64{}
	}
	s.data.LogViewSince[id] = unixMilli
	return s.flushLocked()
}

// logViewSince 读取指定 id 的清空戳；不存在返回 0（表示"显示所有历史"）。
func (s *metaStore) logViewSince(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LogViewSince[id]
}

func filterOut(src []string, drop map[string]struct{}) []string {
	out := src[:0:0]
	for _, x := range src {
		if _, ok := drop[x]; ok {
			continue
		}
		out = append(out, x)
	}
	return out
}
