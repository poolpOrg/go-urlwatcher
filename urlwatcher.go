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

	inProgress    bool
	progressMutex sync.Mutex
}

func newResource(key string, updater func(string) ([]byte, error)) *resource {
	return &resource{
		key:     key,
		updater: updater,
		data:    []byte{},
	}
}

func (r *resource) update(config *Config, broadcast func(string, []byte)) {
	r.progressMutex.Lock()
	if r.inProgress {
		//fmt.Fprintf(os.Stderr, "%s: resource %s fetch already in progress\n", time.Now(), r.key)
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
		if retryDelay > config.ErrorMaxInterval {
			retryDelay = config.ErrorMaxInterval
		}
		if time.Since(r.lastError) < retryDelay {
			return
		}
	} else if time.Since(r.lastRun) < config.RefreshInterval {
		return
	}

	n_attempts := r.n_attempts
	if data, err := r.updater(r.key); err != nil {
		n_attempts++

		// this is for logging purposes only
		//nextTry := time.Duration(0)
		//r.lastError = time.Now()
		//if !r.lastError.IsZero() {
		//	nextTry = (1 << n_attempts) * time.Second
		//	if nextTry > config.ErrorMaxInterval {
		//		nextTry = config.ErrorMaxInterval
		//	}
		//}
		//fmt.Fprintf(os.Stderr, "%s: resource %s errored: %v (attempt:%d, next try in: %s)\n", time.Now(), r.key, err, n_attempts, nextTry)

	} else if !bytes.Equal(data, r.data) {
		checksum := sha256.Sum256(data)
		//if len(r.checksum) == 0 {
		//	//fmt.Printf("%s: resource %s initialized: %x\n", time.Now(), r.key, checksum)
		//} else {
		//	//fmt.Printf("%s: resource %s updated: %x (was: %x)\n", time.Now(), r.key, checksum, r.checksum)
		//}
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

	watcherConfig *Config

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

		watcherConfig: config,

		fetchesSem:   make(chan struct{}, config.MaxParallelFetches),
		callbacksSem: make(chan struct{}, config.MaxParallelCallbacks),
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
			r.fetchesSem <- struct{}{}
			// blocking ? goroutine ?
			nr.update(r.watcherConfig, r.broadcast)
			<-r.fetchesSem
			//fmt.Printf("%s: resource %s added\n", time.Now(), nr.key)

		case nr := <-r.delChannel:
			//fmt.Printf("%s: resource %s deleted\n", time.Now(), nr.key)
			_ = nr

		case <-time.After(r.watcherConfig.TickerInterval):
			r.resourcesMutex.Lock()
			for _, res := range r.resources {
				r.fetchesSem <- struct{}{}
				go func(nr *resource) {
					defer func() { <-r.fetchesSem }()
					nr.update(r.watcherConfig, r.broadcast)
				}(res)
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
				Timeout: r.watcherConfig.RequestTimeout,
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
	r.subscribersMutex.Lock()
	for _, watcher_id := range r.subscribersFromResource[key] {
		r.callbacksSem <- struct{}{}
		go func(_watcher_id string) {
			defer func() { <-r.callbacksSem }()
			r.subscribers[_watcher_id](now, key, data)
		}(watcher_id)
	}
	r.subscribersMutex.Unlock()
}

func (r *ResourceWatcher) Subscribe(key string, callback func(time.Time, string, []byte)) func() {
	watcher_id := uuid.NewString()

	r.subscribersMutex.Lock()
	r.subscribers[watcher_id] = callback
	r.subscriberToResource[watcher_id] = key
	r.subscribersFromResource[key] = append(r.subscribersFromResource[key], watcher_id)
	r.subscribersMutex.Unlock()

	r.resourcesMutex.Lock()
	if res, ok := r.resources[key]; ok {
		if len(res.data) > 0 {
			r.callbacksSem <- struct{}{}
			go func() {
				defer func() { <-r.callbacksSem }()
				callback(time.Now(), key, res.data)
			}()
		}
	}
	r.resourcesMutex.Unlock()

	return func() {
		r.subscribersMutex.Lock()
		delete(r.subscribers, watcher_id)
		delete(r.subscriberToResource, watcher_id)

		watchers := make([]string, 0)
		for _, id := range r.subscribersFromResource[key] {
			if id != watcher_id {
				watchers = append(watchers, id)
			}
		}
		if len(watchers) == 0 {
			delete(r.subscribersFromResource, key)
		} else {
			r.subscribersFromResource[key] = watchers
		}
		r.subscribersMutex.Unlock()
	}
}
