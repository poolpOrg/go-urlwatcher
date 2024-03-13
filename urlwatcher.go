package urlwatcher

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type resource struct {
	key      string
	updater  func(string) ([]byte, error)
	data     []byte
	checksum []byte

	n_attempts int

	lastUpdate time.Time
	lastError  time.Time
	lastRun    time.Time

	inProgress bool
}

func newResource(key string, updater func(string) ([]byte, error)) *resource {
	return &resource{
		key:     key,
		updater: updater,
		data:    []byte{},
	}
}

func fetcher(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (r *resource) update(broadcast func(string, []byte)) {
	if r.inProgress {
		//fmt.Fprintf(os.Stderr, "%s: resource %s fetch already in progress\n", time.Now(), r.key)
		return
	}
	r.inProgress = true
	defer func() { r.inProgress = false }()

	if time.Since(r.lastRun) < 5*time.Second {
		return
	}

	if time.Since(r.lastError) < (1<<r.n_attempts)*time.Second {
		return
	}

	if time.Since(r.lastUpdate) < 15*time.Second {
		return
	}

	n_attempts := r.n_attempts
	if data, err := r.updater(r.key); err != nil {
		nextTry := time.Duration(0)
		r.lastError = time.Now()
		if n_attempts < 10 {
			n_attempts++
		}
		if !r.lastError.IsZero() {
			nextTry = (1 << n_attempts) * time.Second
		}
		_ = nextTry
		//fmt.Fprintf(os.Stderr, "%s: resource %s errored: %v (attempt:%d, next try in: %s)\n", time.Now(), r.key, err, n_attempts, nextTry)
	} else if !bytes.Equal(data, r.data) {
		checksum := sha256.Sum256(data)
		if len(r.checksum) == 0 {
			//	fmt.Printf("%s: resource %s initialized: %x\n", time.Now(), r.key, checksum)
		} else {
			//	fmt.Printf("%s: resource %s updated: %x (was: %x)\n", time.Now(), r.key, checksum, r.checksum)
		}
		r.data = data
		r.checksum = checksum[:]
		r.lastUpdate = time.Now()
		n_attempts = 0
		broadcast(r.key, r.data)
	} else {
		//fmt.Printf("%s: resource %s has not changed: %x\n", time.Now(), r.key, r.checksum)
		n_attempts = 0
	}
	r.n_attempts = n_attempts
	r.lastRun = time.Now()
}

type ResourceWatcher struct {
	resources      map[string]*resource
	resourcesMutex sync.Mutex

	watchers             map[string]func(string, []byte)
	watcherToresource    map[string]string
	watchersFromResource map[string][]string
	watchersMutex        sync.Mutex

	addChannel  chan *resource
	delChannel  chan *resource
	stopChannel chan struct{}
}

func NewWatcher() *ResourceWatcher {
	r := &ResourceWatcher{
		resources: make(map[string]*resource),

		watchers:             make(map[string]func(string, []byte)),
		watcherToresource:    make(map[string]string),
		watchersFromResource: make(map[string][]string),

		addChannel:  make(chan *resource),
		delChannel:  make(chan *resource),
		stopChannel: make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *ResourceWatcher) run() {
	for {
		select {
		case <-r.stopChannel:
			//fmt.Printf("%s: stopping goroutine\n", time.Now())
			return

		case nr := <-r.addChannel:
			nr.update(r.broadcast)
			//fmt.Printf("%s: resource %s added\n", time.Now(), nr.key)

		case nr := <-r.delChannel:
			//fmt.Printf("%s: resource %s deleted\n", time.Now(), nr.key)
			_ = nr

		case <-time.After(1 * time.Second):
			r.resourcesMutex.Lock()
			for _, res := range r.resources {
				go res.update(r.broadcast)
			}
			r.resourcesMutex.Unlock()
		}
	}
}

func (r *ResourceWatcher) Terminate() {
	r.stopChannel <- struct{}{}
}

func (r *ResourceWatcher) Watch(key string) bool {
	r.resourcesMutex.Lock()
	defer r.resourcesMutex.Unlock()
	if _, ok := r.resources[key]; ok {
		return false
	} else {
		r.resources[key] = newResource(key, fetcher)
		r.addChannel <- r.resources[key]
		return true
	}
}

func (r *ResourceWatcher) Unwatch(key string) bool {
	r.resourcesMutex.Lock()
	defer r.resourcesMutex.Unlock()
	if res, ok := r.resources[key]; !ok {
		return false
	} else {
		delete(r.resources, key)
		r.delChannel <- res
		return true
	}
}

func (r *ResourceWatcher) broadcast(key string, data []byte) {
	r.watchersMutex.Lock()
	for _, watcher_id := range r.watchersFromResource[key] {
		r.watchers[watcher_id](key, data)
	}
	r.watchersMutex.Unlock()
}

func (r *ResourceWatcher) Subscribe(key string, callback func(string, []byte)) func() {
	watcher_id := uuid.NewString()

	r.watchersMutex.Lock()
	r.watchers[watcher_id] = callback
	r.watcherToresource[watcher_id] = key
	r.watchersFromResource[key] = append(r.watchersFromResource[key], watcher_id)
	r.watchersMutex.Unlock()

	r.resourcesMutex.Lock()
	if res, ok := r.resources[key]; ok {
		if len(res.data) > 0 {
			callback(key, res.data)
		}
	}
	r.resourcesMutex.Unlock()

	return func() {
		r.watchersMutex.Lock()
		delete(r.watchers, watcher_id)
		delete(r.watcherToresource, watcher_id)

		watchers := make([]string, 0)
		for _, id := range r.watchersFromResource[key] {
			if id != watcher_id {
				watchers = append(watchers, id)
			}
		}
		if len(watchers) == 0 {
			delete(r.watchersFromResource, key)
		} else {
			r.watchersFromResource[key] = watchers
		}
		r.watchersMutex.Unlock()
	}
}
