package main

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff/v4"
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
	"time"
)

func main() {
	ctx := context.Background()
	var chrome *cdp.Client

	retryFunc := func() error {
		var err error
		chrome, err = connectToChromeDebugger(ctx, "http://127.0.0.1:9222")
		if err != nil {
			log.Print(fmt.Errorf("can't connect to http://127.0.0.1:9222. Chrome must be started in debug mode. %w", err))
		}
		return err
	}

	err := backoff.Retry(retryFunc, backoff.NewConstantBackOff(5*time.Second))
	if err != nil {
		log.Fatal(err)
	}

	// Listen to response received events
	responseReceived, err := chrome.Network.ResponseReceived(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// Enable event stream
	if err = chrome.Network.Enable(ctx, network.NewEnableArgs()); err != nil {
		log.Fatal(err)
	}

	// Open netflix tab
	navArgs := page.NewNavigateArgs("https://www.netflix.com")
	_, err = chrome.Page.Navigate(ctx, navArgs)
	if err != nil {
		log.Fatal(err)
	}

	defer responseReceived.Close()

	// Initial queue pool for download jobs
	q := queue.NewPool(8)
	defer q.Release()

	for u := range listen(responseReceived) {
		if !isAudioURL(u) {
			continue
		}

		err := enqueueDownload(q, toDownloadableURL(u), "DL-"+strconv.Itoa(rand.Int()))
		if err != nil {
			log.Println(err)
		}
	}
}

// listen to all responses received by the current tab and send us their URLs.
func listen(responseReceived network.ResponseReceivedClient) chan string {
	urls := make(chan string)
	go func() {
		for {
			select {
			case <-responseReceived.Ready():
				ev, err := responseReceived.Recv()
				if err != nil {
					log.Fatal(err)
				}

				urls <- ev.Response.URL
			}
		}
	}()

	return urls
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

// toDownloadableURL removes the path from the url to make the resource downloadable. In our case the path
// always contains a download-range in bytes which we can discard. See isAudioURL.
func toDownloadableURL(audioURL string) string {
	// We need to remove the path from the audio url to get a downloadable url
	u, err := url.Parse(audioURL)
	if err != nil {
		log.Fatal(err)
	}

	u.Path = ""
	return u.String()

}

func enqueueDownload(q *queue.Queue, fromURL, toPath string) error {
	go func(s, t string) {
		err := q.QueueTask(func(ctx context.Context) error {
			return download(fromURL, toPath)
		})
		if err != nil {
			return
		}

	}(fromURL, toPath)

	return nil
}

func download(fromUrl, toPath string) error {
	log.Println("Downloading " + fromUrl)
	out, err := os.Create(toPath)

	if err != nil {
		return err
	}

	defer out.Close()

	resp, err := http.Get(fromUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	n, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	log.Printf("Done, got %d bytes", n)

	return nil
}
