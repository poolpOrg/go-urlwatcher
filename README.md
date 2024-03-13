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

Upon changes, it can trigger callbacks to notify of the new content for a given URL.


```go
package main

import (
    "fmt"
    "time"
    "crypto/sha256"

    urlwatcher "github.com/poolpOrg/go-urlwatcher"
)

func notifyMe(key string, data []byte) {
	fmt.Printf("content has changed at %s, new checksum: %x\n", key, sha256.Sum256(data))
}

func main() {
    r := urlwatcher.NewWatcher()
    r.Watch("https://poolp.org")
    r.Watch("https://poolp.org/test")

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
```

Once running:
```sh
$ go run main.go
% ./tester 
content has changed at https://poolp.org, new checksum: 2621d0b4ee53a3bc338a62272b173b5c99f860aec93204cb5df3688335d10deb
content has changed at https://poolp.org/test, new checksum: 907bde3816465e678dd2d661bf3d84f933e71c5e2ea25543247df7a5858dfa55
content has changed at https://poolp.org/test, new checksum: bbdf7b8c3cc5267ca09e667e50c5eaa0b7ae206093870a151f5dc8759467486d
```


## What's missing ?

- code cleanup
- configurability of timeouts and other parameters


## Special thanks
This project was worked on partly during my spare time and partly during my work time,
at [VeepeeTech](https://github.com/veepee-oss).


