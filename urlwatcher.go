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
	watcher *ResourceWatcher
	key     string
	//	updater  func(string) ([]byte, error)
	data     []byte
	checksum []byte

	n_attempts int

	lastUpdate time.Time
	lastError  time.Time
	lastRun    time.Time

	inProgress    bool
	progressMutex sync.Mutex
}

func newResource(watcher *ResourceWatcher, key string) *resource {
	return &resource{
		watcher: watcher,
		key:     key,
		data:    []byte{},
	}
}

func (r *resource) updater(url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: timeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	} else {
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		return data, err
	}
}

func (r *resource) update() {
	r.progressMutex.Lock()
	if r.inProgress {
		//fmt.Fprintf(os.Stderr, "%s: resource %s fetch already in progress\n", time.Now(), rw.key)
		r.progressMutex.Unlock()
		return
	}
	r.inProgress = true
	r.progressMutex.Unlock()
	defer func() {
		r.progressMutex.Lock()
		r.inProgress = false
		r.progressMutex.Unlock()
	}()

	// we're retrying an error
	if r.n_attempts != 0 {
		retryDelay := (1 << r.n_attempts) * time.Second
		if retryDelay > r.watcher.config.ErrorMaxInterval {
			retryDelay = r.watcher.config.ErrorMaxInterval
		}
		if time.Since(r.lastError) < retryDelay {
			return
		}
	} else if time.Since(r.lastRun) < r.watcher.config.RefreshInterval {
		return
	}

	n_attempts := r.n_attempts
	if data, err := r.updater(r.key, r.watcher.config.RequestTimeout); err != nil {
		n_attempts++

		// this is for logging purposes only
		//nextTry := time.Duration(0)
		//rw.lastError = time.Now()
		//if !rw.lastErrorw.IsZero() {
		//	nextTry = (1 << n_attempts) * time.Second
		//	if nextTry > config.ErrorMaxInterval {
		//		nextTry = config.ErrorMaxInterval
		//	}
		//}
		//fmt.Fprintf(os.Stderr, "%s: resource %s errored: %v (attempt:%d, next try in: %s)\n", time.Now(), rw.key, err, n_attempts, nextTry)

	} else if !bytes.Equal(data, r.data) {
		checksum := sha256.Sum256(data)
		//if len(rw.checksum) == 0 {
		//	//fmt.Printf("%s: resource %s initialized: %x\n", time.Now(), rw.key, checksum)
		//} else {
		//	//fmt.Printf("%s: resource %s updated: %x (was: %x)\n", time.Now(), rw.key, checksum, rw.checksum)
		//}
		r.data = data
		r.checksum = checksum[:]
		r.lastUpdate = time.Now()
		n_attempts = 0
		r.watcher.broadcast(r.key, r.data)
	} else {
		//fmt.Printf("%s: resource %s has not changed: %x\n", time.Now(), rw.key, rw.checksum)
		n_attempts = 0
	}
	r.n_attempts = n_attempts
	r.lastRun = time.Now()
}

type Config struct {
	RequestTimeout   time.Duration
	RefreshInterval  time.Duration
	ErrorMaxInterval time.Duration
	TickerInterval   time.Duration

	MaxParallelFetches   int
	MaxParallelCallbacks int
}

var DefaultWatcherConfig = Config{
	RequestTimeout:       5 * time.Second,
	RefreshInterval:      15 * time.Minute,
	ErrorMaxInterval:     15 * time.Minute,
	TickerInterval:       1 * time.Second,
	MaxParallelFetches:   10,
	MaxParallelCallbacks: 100,
}

type ResourceWatcher struct {
	resources      map[string]*resource
	resourcesMutex sync.Mutex

	subscribers             map[string]func(time.Time, string, []byte)
	subscriberToResource    map[string]string
	subscribersFromResource map[string][]string
	subscribersMutex        sync.Mutex

	addChannel  chan *resource
	delChannel  chan *resource
	stopChannel chan struct{}

	config *Config

	fetchesSem   chan struct{}
	callbacksSem chan struct{}
}

func NewWatcher(config *Config) *ResourceWatcher {
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
		if config.MaxParallelFetches == 0 {
			config.MaxParallelFetches = DefaultWatcherConfig.MaxParallelFetches
		}
		if config.MaxParallelCallbacks == 0 {
			config.MaxParallelCallbacks = DefaultWatcherConfig.MaxParallelCallbacks
		}
	}

	r := &ResourceWatcher{
		resources: make(map[string]*resource),

		subscribers:             make(map[string]func(time.Time, string, []byte)),
		subscriberToResource:    make(map[string]string),
		subscribersFromResource: make(map[string][]string),

		addChannel:  make(chan *resource),
		delChannel:  make(chan *resource),
		stopChannel: make(chan struct{}),

		config: config,

		fetchesSem:   make(chan struct{}, config.MaxParallelFetches),
		callbacksSem: make(chan struct{}, config.MaxParallelCallbacks),
	}
	go r.run()
	return r
}

func (rw *ResourceWatcher) doUpdate(res *resource) {
	rw.fetchesSem <- struct{}{}
	go func(nr *resource) {
		defer func() { <-rw.fetchesSem }()
		res.update()
	}(res)
}

func (rw *ResourceWatcher) run() {
	for {
		select {
		case <-rw.stopChannel:
			//fmt.Printf("%s: stopping goroutine\n", time.Now())
			return

		case res := <-rw.addChannel:
			rw.doUpdate(res)

		case nr := <-rw.delChannel:
			//fmt.Printf("%s: resource %s deleted\n", time.Now(), nrw.key)
			_ = nr

		case <-time.After(rw.config.TickerInterval):
			rw.resourcesMutex.Lock()
			for _, res := range rw.resources {
				rw.doUpdate(res)
			}
			rw.resourcesMutex.Unlock()
		}
	}
}

func (rw *ResourceWatcher) Terminate() {
	rw.stopChannel <- struct{}{}
}

func (rw *ResourceWatcher) Watch(key string) bool {
	rw.resourcesMutex.Lock()
	defer rw.resourcesMutex.Unlock()
	if _, ok := rw.resources[key]; ok {
		return false
	} else {
		rw.resources[key] = newResource(rw, key)
		rw.addChannel <- rw.resources[key]
		return true
	}
}

func (rw *ResourceWatcher) Unwatch(key string) bool {
	rw.resourcesMutex.Lock()
	defer rw.resourcesMutex.Unlock()
	if res, ok := rw.resources[key]; !ok {
		return false
	} else {
		delete(rw.resources, key)
		rw.delChannel <- res
		return true
	}
}

func (rw *ResourceWatcher) notify(watcher_id string, now time.Time, key string, data []byte) {
	rw.callbacksSem <- struct{}{}
	go func(_watcher_id string) {
		defer func() { <-rw.callbacksSem }()
		rw.subscribers[_watcher_id](now, key, data)
	}(watcher_id)
}

func (rw *ResourceWatcher) broadcast(key string, data []byte) {
	now := time.Now()
	rw.subscribersMutex.Lock()
	for _, watcher_id := range rw.subscribersFromResource[key] {
		rw.notify(watcher_id, now, key, data)
	}
	rw.subscribersMutex.Unlock()
}

func (rw *ResourceWatcher) Subscribe(key string, callback func(time.Time, string, []byte)) func() {
	watcher_id := uuid.NewString()

	rw.subscribersMutex.Lock()
	rw.subscribers[watcher_id] = callback
	rw.subscriberToResource[watcher_id] = key
	rw.subscribersFromResource[key] = append(rw.subscribersFromResource[key], watcher_id)
	rw.subscribersMutex.Unlock()

	rw.resourcesMutex.Lock()
	if res, ok := rw.resources[key]; ok {
		if len(res.data) > 0 {
			rw.callbacksSem <- struct{}{}
			go func() {
				defer func() { <-rw.callbacksSem }()
				callback(time.Now(), key, res.data)
			}()
		}
	}
	rw.resourcesMutex.Unlock()

	return func() {
		rw.subscribersMutex.Lock()
		delete(rw.subscribers, watcher_id)
		delete(rw.subscriberToResource, watcher_id)

		watchers := make([]string, 0)
		for _, id := range rw.subscribersFromResource[key] {
			if id != watcher_id {
				watchers = append(watchers, id)
			}
		}
		if len(watchers) == 0 {
			delete(rw.subscribersFromResource, key)
		} else {
			rw.subscribersFromResource[key] = watchers
		}
		rw.subscribersMutex.Unlock()
	}
}
