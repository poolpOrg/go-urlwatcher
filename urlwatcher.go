package urlwatcher

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
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

func (r *resource) update(config *WatcherConfig, broadcast func(string, []byte)) {
	if r.inProgress {
		fmt.Fprintf(os.Stderr, "%s: resource %s fetch already in progress\n", time.Now(), r.key)
		return
	}
	r.inProgress = true
	defer func() { r.inProgress = false }()

	retryDelay := (1 << r.n_attempts) * time.Second
	if retryDelay > config.ErrorMaxInterval {
		retryDelay = config.ErrorMaxInterval
	}
	if time.Since(r.lastError) < retryDelay {
		return
	}

	if time.Since(r.lastUpdate) < config.RefreshInterval {
		return
	}

	n_attempts := r.n_attempts
	if data, err := r.updater(r.key); err != nil {
		n_attempts++

		// this is for logging purposes only
		nextTry := time.Duration(0)
		r.lastError = time.Now()
		if !r.lastError.IsZero() {
			nextTry = (1 << n_attempts) * time.Second
			if nextTry > config.ErrorMaxInterval {
				nextTry = config.ErrorMaxInterval
			}
		}
		fmt.Fprintf(os.Stderr, "%s: resource %s errored: %v (attempt:%d, next try in: %s)\n", time.Now(), r.key, err, n_attempts, nextTry)

	} else if !bytes.Equal(data, r.data) {
		checksum := sha256.Sum256(data)
		if len(r.checksum) == 0 {
			fmt.Printf("%s: resource %s initialized: %x\n", time.Now(), r.key, checksum)
		} else {
			fmt.Printf("%s: resource %s updated: %x (was: %x)\n", time.Now(), r.key, checksum, r.checksum)
		}
		r.data = data
		r.checksum = checksum[:]
		r.lastUpdate = time.Now()
		n_attempts = 0
		broadcast(r.key, r.data)
	} else {
		fmt.Printf("%s: resource %s has not changed: %x\n", time.Now(), r.key, r.checksum)
		n_attempts = 0
	}
	r.n_attempts = n_attempts
	r.lastRun = time.Now()
}

type WatcherConfig struct {
	RequestTimeout   time.Duration
	RefreshInterval  time.Duration
	ErrorMaxInterval time.Duration
	TickerInterval   time.Duration
}

var DefaultWatcherConfig = WatcherConfig{
	RequestTimeout:   5 * time.Second,
	RefreshInterval:  15 * time.Minute,
	ErrorMaxInterval: 15 * time.Minute,
	TickerInterval:   1 * time.Second,
}

type ResourceWatcher struct {
	resources      map[string]*resource
	resourcesMutex sync.Mutex

	watchers             map[string]func(time.Time, string, []byte)
	watcherToresource    map[string]string
	watchersFromResource map[string][]string
	watchersMutex        sync.Mutex

	addChannel  chan *resource
	delChannel  chan *resource
	stopChannel chan struct{}

	config *WatcherConfig
}

func NewWatcher(config *WatcherConfig) *ResourceWatcher {
	if config == nil {
		config = &DefaultWatcherConfig
	} else {
		if config.RequestTimeout == time.Duration(0) {
			config.RequestTimeout = DefaultWatcherConfig.RequestTimeout
		}
		if config.RefreshInterval == time.Duration(0) {
			config.RefreshInterval = DefaultWatcherConfig.RefreshInterval
		}
		if config.ErrorMaxInterval == time.Duration(0) {
			config.ErrorMaxInterval = DefaultWatcherConfig.ErrorMaxInterval
		}
		if config.TickerInterval == time.Duration(0) {
			config.TickerInterval = DefaultWatcherConfig.TickerInterval
		}
	}

	r := &ResourceWatcher{
		resources: make(map[string]*resource),

		watchers:             make(map[string]func(time.Time, string, []byte)),
		watcherToresource:    make(map[string]string),
		watchersFromResource: make(map[string][]string),

		addChannel:  make(chan *resource),
		delChannel:  make(chan *resource),
		stopChannel: make(chan struct{}),

		config: config,
	}
	go r.run()
	return r
}

func (r *ResourceWatcher) run() {
	for {
		select {
		case <-r.stopChannel:
			fmt.Printf("%s: stopping goroutine\n", time.Now())
			return

		case nr := <-r.addChannel:
			nr.update(r.config, r.broadcast)
			fmt.Printf("%s: resource %s added\n", time.Now(), nr.key)

		case nr := <-r.delChannel:
			fmt.Printf("%s: resource %s deleted\n", time.Now(), nr.key)
			_ = nr

		case <-time.After(1 * time.Second):
			r.resourcesMutex.Lock()
			for _, res := range r.resources {
				go res.update(r.config, r.broadcast)
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
		r.resources[key] = newResource(key, func(url string) ([]byte, error) {
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				return nil, err
			}

			client := &http.Client{
				Timeout: r.config.RequestTimeout,
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
		})
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
	now := time.Now()
	r.watchersMutex.Lock()
	for _, watcher_id := range r.watchersFromResource[key] {
		r.watchers[watcher_id](now, key, data)
	}
	r.watchersMutex.Unlock()
}

func (r *ResourceWatcher) Subscribe(key string, callback func(time.Time, string, []byte)) func() {
	watcher_id := uuid.NewString()

	r.watchersMutex.Lock()
	r.watchers[watcher_id] = callback
	r.watcherToresource[watcher_id] = key
	r.watchersFromResource[key] = append(r.watchersFromResource[key], watcher_id)
	r.watchersMutex.Unlock()

	r.resourcesMutex.Lock()
	if res, ok := r.resources[key]; ok {
		if len(res.data) > 0 {
			callback(time.Now(), key, res.data)
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
