package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/poolpOrg/go-urlwatcher"
)

func init() {
	go func() {
		maxMemory := 0

		for range time.Tick(time.Second) {
			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)

			if int(memStats.Alloc) > maxMemory {
				maxMemory = int(memStats.Alloc)
			}

			fmt.Printf("Time: %v\n", time.Now().UTC().Format(time.RFC822Z))
			fmt.Printf("Number of Goroutines: %d\n", runtime.NumGoroutine())
			fmt.Printf("Memory Allocated: %s (max: %s)\n", humanize.Bytes(memStats.Alloc), humanize.Bytes(uint64(maxMemory)))
			fmt.Printf("Total Memory Allocated: %s\n", humanize.Bytes(memStats.TotalAlloc))
			fmt.Printf("System Memory: %s\n", humanize.Bytes(memStats.Sys))
			fmt.Printf("GC: %d\n", memStats.NumGC)
			// Add more stats here as needed
			fmt.Println("-------------------------------------")
		}
	}()
}

func main() {
	//r := urlwatcher.NewWatcher(&urlwatcher.DefaultWatcherConfig)
	//r := urlwatcher.NewWatcher(nil)
	r := urlwatcher.NewWatcher(&urlwatcher.Config{
		RefreshInterval: 1 * time.Microsecond,
		TickerInterval:  1 * time.Microsecond,
	})
	r.Watch("https://lab.poolp.org/pub/dmesg.txt")
	r.Watch("http://localhost:8012")

	for i := 0; i < 10; i++ {
		//for i := 0; i < 10; i++ {
		go func() {
			// notify me forever of any change in https://lab.poolp.org/pub/dmesg.txt content
			s := r.Subscribe("https://lab.poolp.org/pub/dmesg.txt")

			//go func() { time.Sleep(time.Second); s.Unsubscribe() }()

			for msg := range s.Events() {
				fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
					msg.Timestamp, msg.Key, msg.Checksum)
			}
			fmt.Println("Unsubscribed")
		}()
	}

	// wait forever
	<-make(chan struct{})
}
