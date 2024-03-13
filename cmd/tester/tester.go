package main

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/poolpOrg/go-urlwatcher"
)

func notifyMe(timestamp time.Time, key string, data []byte) {
	fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
		timestamp, key, sha256.Sum256(data))
}

func main() {
	r := urlwatcher.NewWatcher(&urlwatcher.DefaultWatcherConfig)
	//r := urlwatcher.NewWatcher(nil)
	//r := urlwatcher.NewWatcher(&urlwatcher.WatcherConfig{
	//	RequestTimeout: 10 * time.Second,
	//})
	r.Watch("https://poolp.org")
	r.Watch("https://poolp.org/test")
	r.Watch("http://localhost:8012")

	// notify me forever of any change in https://poolp.org content
	r.Subscribe("https://poolp.org", notifyMe)

	// notify me of all changes in https://poolp.org/test ...
	unsubscribe := r.Subscribe("https://poolp.org/test", notifyMe)

	// ... and in a minute, I'll unsubscribe from these events
	time.Sleep(1 * time.Minute)
	unsubscribe()

	// wait forever
	<-make(chan struct{})
}
