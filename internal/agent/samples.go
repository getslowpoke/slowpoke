package agent

import "sync"

// SampleStore keeps the slowest real statement of each query, on this machine
// only, so EXPLAIN can run without the server ever sending or seeing SQL values.
type SampleStore struct {
	mu    sync.Mutex
	max   int
	items map[string]sample
	order []string
}

type sample struct {
	sql     string
	seconds float64
	owner   SampleOwner
}

// SampleOwner says where a statement was seen, so EXPLAIN runs on that database:
// a machine can host MariaDB and PostgreSQL side by side.
type SampleOwner struct {
	Engine string // mysql | postgres
	Source string // id of the polled database source; empty when only the engine is known (slow log)
}

func NewSampleStore(max int) *SampleStore {
	return &SampleStore{max: max, items: map[string]sample{}}
}

func (s *SampleStore) Put(hash, sql string, seconds float64, owner SampleOwner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.items[hash]; ok {
		if seconds >= cur.seconds {
			s.items[hash] = sample{sql, seconds, owner}
		}
		return
	}
	if len(s.order) >= s.max {
		delete(s.items, s.order[0])
		s.order = s.order[1:]
	}
	s.items[hash] = sample{sql, seconds, owner}
	s.order = append(s.order, hash)
}

func (s *SampleStore) Get(hash string) (string, SampleOwner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[hash]
	return v.sql, v.owner, ok
}
