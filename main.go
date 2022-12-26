package main

import (
	"context"
	"fmt"
	"github.com/golang-queue/queue"
	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/network"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/rpcc"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func main() {
	ctx := context.Background()

	chrome, err := connectToChromeDebugger(ctx, "http://127.0.0.1:9222")
	if err != nil {
		log.Fatal(err)
	}

	// Listen to response received events
	responseReceived, err := chrome.Network.ResponseReceived(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Go to netflix
	navArgs := page.NewNavigateArgs("https://www.netflix.com")
	_, err = chrome.Page.Navigate(ctx, navArgs)
	if err != nil {
		log.Fatal(err)
	}

	// Enable event stream
	if err = chrome.Network.Enable(ctx, network.NewEnableArgs()); err != nil {
		log.Fatal(err)
	}

	defer responseReceived.Close()

	// Initial queue pool for download jobs
	q := queue.NewPool(8)
	defer q.Release()

	// Response stream loop
	for {
		select {
		case <-responseReceived.Ready():
			ev, err := responseReceived.Recv()
			if err != nil {
				log.Fatal(err)
			}

			// Ignore non-audio urls
			if !isAudioURL(ev.Response.URL) {
				continue
			}

			go func(srcUrl, tgtPath string) {
				if err := q.QueueTask(newDownloadTask(srcUrl, tgtPath)); err != nil {
					panic(err)
				}
			}(ev.Response.URL, "DL-"+strconv.Itoa(rand.Int()))
		}
	}
}

// connectToChromeDebugger establishes a debugging session on a remote chrome instance. Chrome must be already started in debug-mode.
// See https://blog.chromium.org/2011/05/remote-debugging-with-chrome-developer.html for more details
func connectToChromeDebugger(ctx context.Context, url string) (*cdp.Client, error) {
	// Use the DevTools HTTP/JSON API to manage targets (e.g. pages, webworkers).
	devt := devtool.New(url)
	pt, err := devt.Get(ctx, devtool.Page)
	if err != nil {
		pt, err = devt.Create(ctx)
		if err != nil {
			return nil, err
		}
	}

	// Initiate a new RPC connection to the Chrome DevTools Protocol target.
	conn, err := rpcc.DialContext(ctx, pt.WebSocketDebuggerURL)
	if err != nil {
		return nil, err
	}

	return cdp.NewClient(conn), nil
}

// Audio resources have the path format /range/0-nnnn...
func isAudioURL(u string) bool {
	return strings.Contains(u, "/range/0-")
}

func newDownloadTask(srcUrl string, tgtPath string) queue.TaskFunc {
	return func(ctx context.Context) error {
		downloadAudio(srcUrl, tgtPath)
		return nil
	}
}

func downloadAudio(fromUrl, toPath string) {
	fmt.Println("Downloading " + fromUrl)
	// We need to remove the path from the audio url to get a downloadable url
	u, err := url.Parse(fromUrl)
	if err != nil {
		log.Fatal(err)
	}

	u.Path = ""

	out, err := os.Create(toPath)

	if err != nil {
		log.Fatal(err)
	}

	defer out.Close()

	resp, err := http.Get(u.String())
	if err != nil {
		log.Fatal(err)
	}

	defer resp.Body.Close()

	n, err := io.Copy(out, resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Done, got %d bytes", n)
}
