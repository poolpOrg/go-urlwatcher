package main

import (
	"fmt"
	"time"

	urlwatcher "github.com/poolpOrg/go-urlwatcher"
)

func notifyMe(key string, data []byte) {
	fmt.Println("watcher #########", key, string(data))
}

func main() {
	r := urlwatcher.NewWatcher()

	r.Watch("https://poolp.org/test")
	r.Watch("http://localhost:8012")

	unsubscribe := r.Subscribe("https://poolp.org/test", notifyMe)
	time.Sleep(1 * time.Minute)
	unsubscribe()

	<-make(chan struct{})
}
