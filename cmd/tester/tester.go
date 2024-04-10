package main

import (
	"fmt"
	"runtime"
	"time"

	"github.com/poolpOrg/go-urlwatcher"
)

func humanizeBytes(bytes uint64) string {
	const (
		_         = iota
		kB uint64 = 1 << (10 * iota)
		mB
		gB
		tB
		pB
	)

	switch {
	case bytes < kB:
		return fmt.Sprintf("%dB", bytes)
	case bytes < mB:
		return fmt.Sprintf("%.2fKB", float64(bytes)/float64(kB))
	case bytes < gB:
		return fmt.Sprintf("%.2fMB", float64(bytes)/float64(mB))
	case bytes < tB:
		return fmt.Sprintf("%.2fGB", float64(bytes)/float64(gB))
	case bytes < pB:
		return fmt.Sprintf("%.2fTB", float64(bytes)/float64(tB))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func initStats() {
	maxMemory := 0

	for range time.Tick(time.Second) {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		if int(memStats.Alloc) > maxMemory {
			maxMemory = int(memStats.Alloc)
		}

		fmt.Printf("Time: %v\n", time.Now().UTC().Format(time.RFC822Z))
		fmt.Printf("Number of Goroutines: %d\n", runtime.NumGoroutine())
		fmt.Printf("Memory Allocated: %s (max: %s)\n", humanizeBytes(memStats.Alloc), humanizeBytes(uint64(maxMemory)))
		fmt.Printf("Total Memory Allocated: %s\n", humanizeBytes(memStats.TotalAlloc))
		fmt.Printf("System Memory: %s\n", humanizeBytes(memStats.Sys))
		fmt.Printf("GC: %d\n", memStats.NumGC)
		// Add more stats here as needed
		fmt.Println("-------------------------------------")
	}
}

func init() {
	go initStats()
}

func notifyMe(timestamp time.Time, key string, checksum [32]byte, data []byte) {
	//fmt.Printf("%s: content has changed at %s, new checksum: %x\n",
	//	timestamp, key, sha256.Sum256(data))
}

func subscriberTest(rw *urlwatcher.ResourceWatcher) {
	// notify me forever of any change in https://lab.poolp.org/pub/dmesg.txt content
	//unsubscribe := rw.Subscribe("https://lab.poolp.org/pub/dmesg.txt", notifyMe)
	//close(unsubscribe)
	notifyChan := rw.Subscribe("https://lab.poolp.org/pub/dmesg.txt", notifyMe)
	//for msg := range notifyChan {
	//
	//}
	close(notifyChan)
}

func main() {
	//r := urlwatcher.NewWatcher(&urlwatcher.DefaultWatcherConfig)
	//r := urlwatcher.NewWatcher(nil)
	rw := urlwatcher.NewWatcher(&urlwatcher.Config{
		RefreshInterval: 1 * time.Microsecond,
		TickerInterval:  1 * time.Microsecond,
	})
	rw.Watch("https://lab.poolp.org/pub/dmesg.txt")
	rw.Watch("http://localhost:8012")

	for {
		subscriberTest(rw)
	}

	// wait forever
	<-make(chan struct{})
}
