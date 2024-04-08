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

type Event struct {
	Timestamp time.Time
	Key       string
	Checksum  [32]byte
	Data      []byte
}

type resource struct {
	watcher *ResourceWatcher

	key string
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

func (r *resource) updater(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: r.watcher.config.RequestTimeout,
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

func (r *resource) update(broadcast func(string, [32]byte, []byte)) {
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
		broadcast(r.key, checksum, r.data)
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

	subscribers             map[string]chan Event
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

	rw := &ResourceWatcher{
		resources: make(map[string]*resource),

		subscribers:             make(map[string]chan Event),
		subscriberToResource:    make(map[string]string),
		subscribersFromResource: make(map[string][]string),

		addChannel:  make(chan *resource),
		delChannel:  make(chan *resource),
		stopChannel: make(chan struct{}),

		config: config,

		fetchesSem:   make(chan struct{}, config.MaxParallelFetches),
		callbacksSem: make(chan struct{}, config.MaxParallelCallbacks),
	}
	go rw.run()
	return rw
}

func (rw *ResourceWatcher) run() {
	for {
		select {
		case <-rw.stopChannel:
			//fmt.Printf("%s: stopping goroutine\n", time.Now())
			return

		case nr := <-rw.addChannel:
			rw.fetchesSem <- struct{}{}
			// blocking ? goroutine ?
			nr.update(rw.broadcast)
			<-rw.fetchesSem
			//fmt.Printf("%s: resource %s added\n", time.Now(), nr.key)

		case nr := <-rw.delChannel:
			//fmt.Printf("%s: resource %s deleted\n", time.Now(), nr.key)
			_ = nr

		case <-time.After(rw.config.TickerInterval):
			rw.resourcesMutex.Lock()
			for _, res := range rw.resources {
				rw.fetchesSem <- struct{}{}
				go func(nr *resource) {
					defer func() { <-rw.fetchesSem }()
					nr.update(rw.broadcast)
				}(res)
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

func (rw *ResourceWatcher) broadcast(key string, checksum [32]byte, data []byte) {
	now := time.Now()
	rw.subscribersMutex.Lock()
	for _, watcher_id := range rw.subscribersFromResource[key] {
		rw.callbacksSem <- struct{}{}
		go func(_watcher_id string) {
			defer func() { <-rw.callbacksSem }()
			rw.subscribers[_watcher_id] <- Event{
				Timestamp: now,
				Key:       key,
				Checksum:  checksum,
				Data:      data,
			}
		}(watcher_id)
	}
	rw.subscribersMutex.Unlock()
}

func (rw *ResourceWatcher) unsubscribe(watcher_id string, key string) {
	rw.subscribersMutex.Lock()
	close(rw.subscribers[watcher_id])
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

type Subscription struct {
	rw        *ResourceWatcher
	watcherId string
	key       string
	events    chan Event
}

func NewSubscription(rw *ResourceWatcher, watcher_id string, key string) *Subscription {
	return &Subscription{
		rw:        rw,
		watcherId: watcher_id,
		key:       key,
		events:    make(chan Event),
	}
}
func (s *Subscription) Events() <-chan Event {
	return s.events
}
func (s *Subscription) Unsubscribe() {
	s.rw.unsubscribe(s.watcherId, s.key)
}

func (rw *ResourceWatcher) Subscribe(key string) *Subscription {
	watcher_id := uuid.NewString()

	subscription := NewSubscription(rw, watcher_id, key)

	rw.subscribersMutex.Lock()
	rw.subscribers[watcher_id] = subscription.events
	rw.subscriberToResource[watcher_id] = key
	rw.subscribersFromResource[key] = append(rw.subscribersFromResource[key], watcher_id)
	rw.subscribersMutex.Unlock()

	rw.resourcesMutex.Lock()
	if res, ok := rw.resources[key]; ok {
		if len(res.data) > 0 {
			rw.callbacksSem <- struct{}{}
			go func() {
				defer func() { <-rw.callbacksSem }()
				subscription.events <- Event{
					Timestamp: time.Now(),
					Key:       key,
					Checksum:  [32]byte(res.checksum),
					Data:      res.data,
				}
			}()
		}
	}
	rw.resourcesMutex.Unlock()

	return subscription
}
