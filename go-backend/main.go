package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

type ScrapeRequest struct {
	ArrivalDate   string   `json:"arrivalDate"`
	DepartureDate string   `json:"departureDate"`
	URLs          []string `json:"urls"`
}

type ScrapeResult struct {
	URL   string `json:"url"`
	Price string `json:"price"`
}

var cachedURLs []string

func readURLs() ([]string, error) {
	file, err := os.Open("list.txt")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var urls []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			urls = append(urls, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return urls, nil
}

func withCORS(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3001")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			return
		}

		handler(w, r)
	}
}

func scrapeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ScrapeRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var urls []string
	if len(req.URLs) > 0 {
		urls = req.URLs
	} else {
		urls = cachedURLs
	}

	l := launcher.New().Headless(true).NoSandbox(true)
	url := l.MustLaunch()
	browser := rod.New().ControlURL(url).MustConnect()
	defer browser.MustClose()

	var wg sync.WaitGroup
	resultChan := make(chan ScrapeResult, len(urls))
	semaphore := make(chan struct{}, 5)
	// priceRegex := regexp.MustCompile(`\$\d{0,3}(?:,\d{3})*(?:\.\d{2})?`)
	//  \$\d{0,3}(?:,\d{3})*(?:\.\d{2})?
	//  \$\d{1,3}(?:,\d{3})*(\.\d{2})?
	//  \$\d{1,3}(,\d{3})+\.\d{2}
	for _, baseURL := range urls {
		baseURL := baseURL
		wg.Add(1)
		semaphore <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()

			fullURL := fmt.Sprintf("%s?checkin=%s&checkout=%s", baseURL, req.ArrivalDate, req.DepartureDate)
			page := browser.MustPage(fullURL)
			defer page.MustClose()

			page.MustWaitLoad()
			time.Sleep(5 * time.Second)

			var bestPrice string
			hels, _ := page.Elements(".pdp-quote-total span")
			for _, el := range hels {
				txt := strings.TrimSpace(el.MustText())
				// if priceRegex.MatchString(txt) {
				// 	match := priceRegex.FindString(txt)
				// 	bestPrice = match
				// }
				if strings.HasPrefix(txt, "$") {
					bestPrice = txt
				}
			}

			if bestPrice == "" {
				bestPrice = "N/A"
			}

			resultChan <- ScrapeResult{URL: fullURL, Price: bestPrice}
		}()
	}

	wg.Wait()
	close(resultChan)

	var results []ScrapeResult
	for r := range resultChan {
		results = append(results, r)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func main() {
	var err error
	cachedURLs, err = readURLs()
	if err != nil {
		fmt.Println("❌ Failed to load list.txt:", err)
		os.Exit(1)
	}

	http.HandleFunc("/scrape", withCORS(scrapeHandler))
	fmt.Println("🚀 Server started at http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}
