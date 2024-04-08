# go-urlwatcher

WIP: this is a work in progress

## Should I use it ?

I don't care.


## What is go-urlwatcher ?

go-urlwatcher is a tool to watch URLs and notify you when their content changes.


## What's the license for go-urlwatcher ?

go-urlwatcher is published under the ISC license,
do what you want with it but keep the copyright in place and don't complain if code blows up.

```
Copyright (c) 2024 Gilles Chehade <gilles@poolp.org>

Permission to use, copy, modify, and distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

## How does it work ?

A watcher keeps track of a list of URLs and will periodically fetch their content to check if it has changed.

Upon error, it will retry with a capped exponential backoff.

Upon changes, it can trigger callbacks to notify of the new content for a given URL.


```go
package main

import (
    "fmt"
    "time"
    "crypto/sha256"

    urlwatcher "github.com/poolpOrg/go-urlwatcher"
)

func notifyMe(timestamp time.Time, key string, data []byte) {
    fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
        timestamp, key, sha256.Sum256(data))
}

func main() {
    r := urlwatcher.NewWatcher(&urlwatcher.DefaultWatcherConfig)
    r.Watch("https://poolp.org")
    r.Watch("https://poolp.org/test")


    // notify me forever of any change in https://poolp.org content
    go func() {
        eventChan, _ := r.Subscribe("https://poolp.org")
        for event := range eventChan {
            fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
                msg.Timestamp, msg.Key, msg.Checksum)
        }
    }()


    // notify me of all changes in https://poolp.org/test ...
    eventChan2, unsubscribe := r.Subscribe("https://poolp.org/test", notifyMe)

    // ... and in a minute, I'll unsubscribe from these events
    go func() { time.Sleep(1*time.Minute); unsubscribe() }()

    for event := range eventChan2 {
        fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
            msg.Timestamp, msg.Key, msg.Checksum)
    }
    fmt.Println("Unsubscribed !")

    // wait forever
    <-make(chan struct{})
}
```

Once running:
```sh
$ go run main.go
% ./tester 
2024-03-13 14:03:42.917877 +0100 CET m=+0.159545001: content has changed at https://poolp.org, new checksum: 2621d0b4ee53a3bc338a62272b173b5c99f860aec93204cb5df3688335d10deb
2024-03-13 14:03:42.937477 +0100 CET m=+0.179145876: content has changed at https://poolp.org/test, new checksum: bbdf7b8c3cc5267ca09e667e50c5eaa0b7ae206093870a151f5dc8759467486d
2024-03-13 14:03:58.019213 +0100 CET m=+15.261034210: content has changed at https://poolp.org/test, new checksum: 907bde3816465e678dd2d661bf3d84f933e71c5e2ea25543247df7a5858dfa55

```


The default values for the configuration are:
```go
Config {
    RequestTimeout:   5 * time.Second,
    RefreshInterval:  15 * time.Minute,
    ErrorMaxInterval: 15 * time.Minute,
    TickerInterval:   1 * time.Second,

    MaxParallelFetches: 10,
    MaxParallelCallbacks: 100,
}
```

Where `.RequestTimeout` is the timeout for the entire HTTP request,
`.RefreshInterval` is the interval at which the watcher will attempt to refresh the content of the URLs,
`.ErrorMaxInterval` is the maximum interval at which the watcher will retry a failed request considering
that is performs an exponential backoff capped at this value,
and `.TickerInterval` is the minimal interval at which the watcher will check for new events.

The `.MaxParallelFetches` is the maximum number of parallel fetches that are triggered concurrently,
on different URLs.

The `.MaxParallelCallbacks` is the maximum number of parallel callbacks that are triggered concurrently,
if there are more than this number of callbacks to trigger,
the watcher will queue them and trigger the exceeding ones as the previous ones complete.
This is to allow setting a very large number of callbacks without implying a similar number of goroutines.


## Special thanks
This project was worked on partly during my spare time and partly during my work time,
at [VeepeeTech](https://github.com/veepee-oss).


