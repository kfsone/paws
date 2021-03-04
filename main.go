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

var empowerment = []string{
	"a box of cats",
	"caffeine",
	"Yorkshire tea",
	"Meg's fury",
	"guineapoop",
	"a vision of a world where no bark is left unwoofed",
	"31 guineapigs and counting",
	"your passion",
	"electricity",
	"power",
	"walks",
	"electro-chemical energy generated by monkeys dancing on typewriters",
	"... the dark side of the sun. Deep, huh?",
	"candy",
	"the promise that, tomorrow, there will be candy...",
	"walkies",
	"treats",
	"kibble",
	"the mysteries of science, especially that one about the lead balloon...",
	"mystical energies that cannot be understood with the mind alone, only by seeing beyond the veil and reaching into infinity to discover the pervasive energy that binds and connects us all. The force. I'm talking about the force. I'm a nerd. I wrote a footer on a webpage. Of course I'm a nerd.",
	"breakfast(**).\n\n\n\n\n\n\n\n<small>** It really <i> the most important meal of the day.</small>"
}

type Finder func([]byte) map[string]string

type Crawl struct {
	Site    string
	Page    string
	Headers map[string]string
	Finder  Finder
	Animals map[string]string
}

type AnimalInfo struct {
	Id string
	Sites map[string]string
}

func (c *Crawl) Url() string { return c.Site + c.Page }

func NewCrawl(site, page string, headers map[string]string, finder Finder) *Crawl {
	return &Crawl{Site: site, Page: page, Headers: headers, Finder: finder}
}

func readBody(encoding string, body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	if encoding == "gzip" {
		reader, err := gzip.NewReader(body)
		if err != nil {
			return nil, err
		}
		return ioutil.ReadAll(reader)
	} else {
		return ioutil.ReadAll(body)
	}
}

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
	body, err := readBody(resp.Header.Get("Content-Encoding"), resp.Body)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	c.Animals = c.Finder(body)

	return nil
}

func newRegexFinder(re *regexp.Regexp) Finder {
	return func(data []byte) map[string]string {
		re := re
		matches := make(map[string]string)
		for _, found := range re.FindAllSubmatch(data, -1) {
			href, id := string(found[1]), string(found[2])
			matches[id] = href
		}
		return matches
	}
}

type AdoptaStruct struct {
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

func petFinder(body []byte) map[string]string {
	response := AdoptaStruct{}
	if err := json.Unmarshal(body, &response); err != nil {
		panic(err)
	}
	animals := make(map[string]string)
	for _, animal := range response.Result.Animals {
		animals[animal.Animal.PetId] = animal.Animal.Social.Link
	}
	return animals
}

var seacaaRex = regexp.MustCompile(`"(/adoptions/view-our-animals?[^"]*pet_id=(\d{2,}-\d{5,}))"`)
var adoptaRex = regexp.MustCompile(`href="([^"]+)"[^>]*>.*?<\w+ class="[^"]*periodic-base[^"]*"[^>]*>\s*(\d{2,}-\d{5,})\s*<`)
var petHeaders = map[string]string{
	"Accept":           "application/json, text/plain, */*",
	"X-Requested-With": "XMLHttpRequest",
	"Accept-Encoding":  "gzip, deflate, br",
}

func shorten(sitename string) string {
	l := strings.Index(sitename, ":/") + 3
	return `<a href="`+sitename+`" target="_blank">` + sitename[l:] + `</a>`
}

func runCrawl(w io.Writer) {
	var 	crawls = []*Crawl{
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=0", nil, newRegexFinder(seacaaRex)),
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=1", nil, newRegexFinder(seacaaRex)),
		NewCrawl("https://www.seaaca.org", "/adoptions/view-our-animals/?&page=2", nil, newRegexFinder(seacaaRex)),
		NewCrawl("https://www.adoptapet.com", "/adoption_rescue/73843-seaaca-southeast-area-animal-control-authority-downey-california", nil, newRegexFinder(adoptaRex)),
		NewCrawl("https://www.petfinder.com", "/search/?page=1&limit[]=40&status=adoptable&distance[]=Anywhere&sort[]=recently_added&shelter_id[]=CA990&include_transportable=true", petHeaders, petFinder),
	}

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

	wg.Wait()

	// Alpha list of names
	siteNames := make([]string, 0, len(crawls))
	// Pet IDs
	petIds := make([]string, 0)
	// per-pet list of per-site links
	petSites := make(map[string]map[string]string)
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
			siteNames = append(siteNames, site)
		}
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
	sort.Strings(siteNames)
	sort.Strings(petIds)

	// pets by id followed by siteNames-ordered list of hit/miss
	type AnimalInfo struct {
		Links []string
		PresenceCount int
	}
	pets := make(map[string]AnimalInfo)
	for id, petLinks := range petSites {
		info := AnimalInfo{Links: make([]string, len(siteNames))}
		for idx, site := range siteNames {
			info.Links[idx] = petLinks[site]
			if info.Links[idx] != "" {
				info.PresenceCount++
			}
		}
		pets[id] = info
	}

	rand.Shuffle(len(empowerment), func (l, r int) { empowerment[l], empowerment[r] = empowerment[r], empowerment[l] })

	tpl, err := ioutil.ReadFile("template.gohtml")
	if err != nil {
		panic(err)
	}
	var pageTemplate = template.Must(template.New("pet-page").Parse(string(tpl)))
	err = pageTemplate.Execute(w, &struct{
		Generated string
		Sites []string
		Pets map[string]AnimalInfo
		PoweredBy string
	} {
		Generated: time.Now().Format("Mon 2006/01/02 15:04:05"),
		Sites: siteNames,
		Pets: pets,
		PoweredBy: empowerment[0],
	})
	if err != nil {
		panic(err)
	}
}

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	runCrawl(os.Stdout)
}
