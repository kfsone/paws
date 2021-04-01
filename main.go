// Search multiple sites for matching pet-ids to detect listings that are not
// on all the sites.
package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
)

// AnimalMap is a mapping of petid->url for a set of animals.
type AnimalMap map[string]string

// Finder is a callback that takes the body of a webpage and extracts a mapping of
// petid(string) -> peturl(string).
type Finder func([]byte) AnimalMap

// Crawl represents a url to be crawled and the animals found from doing so.
type Crawl struct {
	// Site is the uri for accessing the top of the site, e.g. http://foo.com
	Site    string
	// Page is the uri for the page to crawl, excluding the site name. e.g "pets/index.html"
	Page    string
	// Headers is an optional table of additional headers to send when making the web request.
	Headers map[string]string
	// Finder is the function to translate pages into petid tables.
	Finder  Finder
	// Animals will be the pet id table returned by Finder.
	Animals map[string]string
}

// Lazy regular expressions to find the IDs for seaca and adoptapet

var seaacaRex = regexp.MustCompile(`"(/adoptions/view-our-animals?[^"]*pet_id=(\d{2,}-\d{5,}))"`)
var adoptaRex = regexp.MustCompile(`href="([^"]+)"[^>]*>.*?<\w+ class="[^"]*periodic-base[^"]*"[^>]*>\s*(\d{2,}-\d{5,})\s*<`)

// petfinder requires some additional headers to actually return a json result.
var petfinderHeaders = map[string]string{
	"Accept":           "application/json, text/plain, */*",
	"X-Requested-With": "XMLHttpRequest",
	"Accept-Encoding":  "gzip, deflate, br",
}

// Url returns the complete URL to retrieve the page ffor a given Crawl.
func (c *Crawl) Url() string { return c.Site + c.Page }

// Run will fetch, decode and extract the pet id table for the page of a given crawl.
func (c *Crawl) Run() error {
	client := &http.Client{}
	req, err := http.NewRequest("GET", c.Url(), nil)
	if err != nil {
		return fmt.Errorf("req: %w", err)
	}
	if c.Headers != nil {
		for hdr, value := range c.Headers {
			req.Header.Add(hdr, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %w", resp.Status, err)
	}
	body, err := decode(resp.Header.Get("Content-Encoding"), resp.Body)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	c.Animals = c.Finder(body)

	return nil
}

// NewCrawl will construct a Crawl object for the given site and page.
func NewCrawl(site, page string, headers map[string]string, finder Finder) *Crawl {
	return &Crawl{Site: site, Page: page, Headers: headers, Finder: finder}
}

// decode will return the full text of a reader, including decompressing the
// content if it is encoded with gzip.
func decode(encoding string, content io.ReadCloser) ([]byte, error) {
	defer content.Close()
	if encoding == "gzip" {
		reader, err := gzip.NewReader(content)
		if err != nil {
			return nil, err
		}
		return ioutil.ReadAll(reader)
	} else {
		return ioutil.ReadAll(content)
	}
}

// newRegexFinder is a Finder callback that applies a regex to a crawled page.
func newRegexFinder(re *regexp.Regexp) Finder {
	return func(data []byte) AnimalMap {
		re := re
		matches := make(AnimalMap)
		for _, found := range re.FindAllSubmatch(data, -1) {
			href, id := string(found[1]), string(found[2])
			matches[id] = href
		}
		return matches
	}
}

// PetfinderSchema is a surgical subset of petfinder's animal-query json.
type PetfinderSchema struct {
	Result struct {
		Animals []struct {
			Animal struct {
				PetId  string `json:"organization_animal_identifier"`
				Social struct {
					Link string `json:"email_url"`
				} `json:"social_sharing"`
			} `json:"animal"`
		} `json:"animals"`
	} `json:"result"`
}

// petFinder is a Finder that leverage's PetFinder's json query to get a pet list.
func petFinder(body []byte) AnimalMap {
	response := PetfinderSchema{}
	if err := json.Unmarshal(body, &response); err != nil {
		panic(err)
	}
	animals := make(AnimalMap)
	for _, animal := range response.Result.Animals {
		animals[animal.Animal.PetId] = animal.Animal.Social.Link
	}
	return animals
}

// shorten is a quick helper to reduce a full sitename down to a prettified link.
func shorten(sitename string) string {
	l := strings.Index(sitename, ":/") + 3
	return `<a href="`+sitename+`" target="_blank">` + sitename[l:] + `</a>`
}

// entry point to run all the calls and aggregate the information.
func runCrawl(w io.Writer) {
	// record generation time.
	generated := time.Now().Format("Mon 2006/01/02 15:04:05")

	// table of sites/pages we are going to visit. seaaca is split across three pages.
	var crawls = []*Crawl{
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=0", nil, newRegexFinder(seaacaRex)),
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=1", nil, newRegexFinder(seaacaRex)),
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=2", nil, newRegexFinder(seaacaRex)),
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=3", nil, newRegexFinder(seaacaRex)),
		NewCrawl("https://www.adoptapet.com", "/adoption_rescue/73843-seaaca-southeast-area-animal-control-authority-downey-california", nil, newRegexFinder(adoptaRex)),
		NewCrawl("https://www.petfinder.com", "/search/?page=1&limit[]=40&status=adoptable&distance[]=Anywhere&sort[]=recently_added&shelter_id[]=CA990&include_transportable=true", petfinderHeaders, petFinder),
	}

	// invoke each crawl in its own worker ('go').
	var wg sync.WaitGroup
	for _, crawl := range crawls {
		crawl := crawl
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := crawl.Run(); err != nil {
				fmt.Printf("ERROR: %s: %s\n", crawl.Url(), err.Error())
			}
		}()
	}

	// wait for the workers to finish.
	wg.Wait()

	// Alpha list of names
	siteNames := make([]string, 0, len(crawls))
	// Pet IDs
	petIds := make([]string, 0)
	// per-pet list of per-site links
	petSites := make(map[string]AnimalMap)

	// go over all the result sets and build a master pet-id table,
	// and links to each pet.
	for _, crawl := range crawls {
		found := false
		site := shorten(crawl.Site)
		for _, prev := range siteNames {
			if prev == site {
				found = true
				break
			}
		}
		if !found {
			// first time a result was rescorded for this site, create
			// a slot for it.
			siteNames = append(siteNames, site)
		}
		// merge the pets into the master list.
		for pet, link := range crawl.Animals {
			if _, exists := petSites[pet]; !exists {
				petSites[pet] = make(map[string]string)
				petIds = append(petIds, pet)
			}
			if !strings.HasPrefix(link, "http") {
				link = crawl.Site + link
			}
			petSites[pet][site] = link
		}
	}
	// alpha-ordered list of sites
	sort.Strings(siteNames)
	// alpha-ordered list of ids.
	sort.Strings(petIds)

	// pets by id followed by siteNames-ordered list of hit/miss
	type AnimalInfo struct {
		Id string
		Links []string
		PresenceCount int
	}
	pets := make([]AnimalInfo, 0, len(petSites))
	for id, petLinks := range petSites {
		info := AnimalInfo{Id: id, Links: make([]string, len(siteNames))}
		for idx, site := range siteNames {
			info.Links[idx] = petLinks[site]
			if info.Links[idx] != "" {
				info.PresenceCount++
			}
		}
		pets = append(pets, info)
	}

	// finally, sort the results by low-to-high presence followed by id. this
	// means the list will show the pets with the least listings first, and make
	// it easier to find omissions since they will be grouped together.
	sort.Slice(pets, func (l, r int) bool {
		switch {
		case pets[l].PresenceCount < pets[r].PresenceCount:
			return true
		case pets[l].PresenceCount == pets[r].PresenceCount:
			return pets[l].Id < pets[r].Id
		default:
			return false
		}
	})

	// the html for the page is stored as a go text/template.
	tpl, err := ioutil.ReadFile("template.txt")
	if err != nil {
		panic(err)
	}
	var pageTemplate = template.Must(template.New("pet-page").Parse(string(tpl)))

	// generate the html
	err = pageTemplate.Execute(w, &struct{
		Generated string
		Sites []string
		Pets []AnimalInfo
		PoweredBy string
	} {
		Generated: generated,
		Sites: siteNames,
		Pets: pets,
		PoweredBy: poweredBy(),  // defined in a separate file
	})
	if err != nil {
		panic(err)
	}

	// fin.
}


func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	runCrawl(os.Stdout)
}

