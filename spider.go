package gscrapy

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"github.com/yhat/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type SpiderStyle uint32

const (
	Basic SpiderStyle = iota
)

type Spider interface {
	Crawl([]string, *Options, ...func(*http.Request))
	Parse(<-chan *html.Node) <-chan Item
	Write(w io.Writer) error
}

type BaseSpider struct {
	Name           string
	AllowedDomains []string
	StartURLs      []string
	Options        *Options
	Item           Item
	items          <-chan Item
}

func newDefaultSpider() (*BaseSpider, error) {
	opt := NewOptions()
	item, err := NewItem("title", "meta", "h1")
	if err != nil {
		return nil, err
	}
	spider := &BaseSpider{
		Options: opt,
		Item:    item,
	}
	spider.Name = spider.Options.BotName
	return spider, nil
}

func NewSpider(style ...SpiderStyle) (*BaseSpider, error) {
	if len(style) > 0 {
		// Last style counts
		switch style[len(style)-1] {
		case 0:
			return newDefaultSpider()
		default:
			break
		}
	}
	return newDefaultSpider()
}

func prepRequest(method, url string, opt *Options) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if opt != nil {
		if opt.Request != nil {
			req = opt.Request
		}
		if len(opt.BotName) > 0 {
			req.Header.Set("user-agent", fmt.Sprintf(
				opt.UserAgentFormat, opt.BotName, opt.Contact))
		}
	}
	return req, nil
}

func respGen(urls []string, opt *Options) <-chan *http.Response {
	_ = runtime.GOMAXPROCS(runtime.NumCPU())
	var wg sync.WaitGroup
	out := make(chan *http.Response)
	wg.Add(len(urls))
	for _, url := range urls {
		go func(url string) {
			req, err := prepRequest("GET", url, opt)
			if err != nil {
				log.Fatalln(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Fatalln(err)
			}
			out <- resp
			wg.Done()
		}(url)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func rootGen(in <-chan *http.Response) <-chan *html.Node {
	_ = runtime.GOMAXPROCS(runtime.NumCPU())
	var wg sync.WaitGroup
	out := make(chan *html.Node)
	for resp := range in {
		wg.Add(1)
		go func(resp *http.Response) {
			root, err := html.Parse(resp.Body)
			if err != nil {
				log.Fatalln(err)
			}
			out <- root
			wg.Done()
		}(resp)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func (sp *BaseSpider) Parse(in <-chan *html.Node) <-chan Item {
	var wg sync.WaitGroup
	out := make(chan Item)
	for root := range in {
		wg.Add(1)
		go func(r *html.Node) {
			for key := range sp.Item {
				key := strings.ToLower(key)
				field := atom.Lookup([]byte(key))
				node, ok := scrape.Find(r, scrape.ByTag(field))
				if ok {
					sp.Item.Add(key, node)
				}
				out <- sp.Item
			}
			wg.Done()
		}(root)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func (sp *BaseSpider) Crawl() <-chan Item {
	items := sp.Parse(rootGen(respGen(sp.StartURLs, sp.Options)))
	sp.items = items
	return items
}

// Write writes data corresponding to sp.Item as JSON bytes to Writer w.
func (sp *BaseSpider) Write(w io.Writer) error {
	for item := range sp.items {
		err := item.Write(w)
		if err != nil {
			return err
		}
	}
	return nil
}
