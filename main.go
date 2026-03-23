package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"time"
	"io"
	"strings"
)

const (
	catalogURL = "https://a.4cdn.org/wsg/catalog.json"
	threadURL  = "https://a.4cdn.org/wsg/thread/%d.json"
	mediaBase  = "https://i.4cdn.org/wsg/"
	threadLink = "https://boards.4chan.org/wsg/thread/%d#p%d"
)

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

type CatalogPage struct {
	Threads []CatalogThread `json:"threads"`
}

type CatalogThread struct {
	No int `json:"no"`
}

type Thread struct {
	Posts []Post `json:"posts"`
}

type Post struct {
	No      int    `json:"no"`
	Filename string `json:"filename"`
	Ext     string `json:"ext"`
	Tim     int64  `json:"tim"`
	Com     string `json:"com"` // HTML comment body
}

type Reply struct {
	PostNo int    `json:"post_no"`
	Text   string `json:"text"`
}

type VideoResponse struct {
	URL      string  `json:"url"`
	Filename string  `json:"filename"`
	ThreadID int     `json:"thread_id"`
	PostNo   int     `json:"post_no"`
	ThreadURL string `json:"thread_url"`
	Replies  []Reply `json:"replies"`
}

func stripHTML(s string) string {
	// Replace <br> variants with newlines first
	brRe := regexp.MustCompile(`(?i)<br\s*/?>`)
	s = brRe.ReplaceAllString(s, "\n")
	// Remove remaining tags
	s = htmlTagRe.ReplaceAllString(s, "")
	return s
}

func getThreadIDs() ([]int, error) {
	resp, err := http.Get(catalogURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch catalog: %w", err)
	}
	defer resp.Body.Close()

	var pages []CatalogPage
	if err := json.NewDecoder(resp.Body).Decode(&pages); err != nil {
		return nil, fmt.Errorf("failed to decode catalog: %w", err)
	}

	var ids []int
	for _, page := range pages {
		for _, thread := range page.Threads {
			ids = append(ids, thread.No)
		}
	}
	return ids, nil
}

func getThreadData(threadID int) (*Thread, error) {
	url := fmt.Sprintf(threadURL, threadID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil
	}

	var thread Thread
	if err := json.NewDecoder(resp.Body).Decode(&thread); err != nil {
		return nil, err
	}
	return &thread, nil
}

// getRepliesTo returns all posts that quote postNo
func getRepliesTo(posts []Post, postNo int) []Reply {
	quotePattern := regexp.MustCompile(fmt.Sprintf(`#p%d`, postNo))
	var replies []Reply
	for _, p := range posts {
		if p.No == postNo {
			continue
		}
		if quotePattern.MatchString(p.Com) {
			replies = append(replies, Reply{
				PostNo: p.No,
				Text:   stripHTML(p.Com),
			})
		}
	}
	return replies
}

func randomVideo() (VideoResponse, error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	threadIDs, err := getThreadIDs()
	if err != nil {
		return VideoResponse{}, err
	}

	rng.Shuffle(len(threadIDs), func(i, j int) {
		threadIDs[i], threadIDs[j] = threadIDs[j], threadIDs[i]
	})

	for _, id := range threadIDs {
		thread, err := getThreadData(id)
		if err != nil || thread == nil {
			continue
		}

		// Collect all video posts
		var videoPosts []Post
		for _, p := range thread.Posts {
			if p.Ext == ".webm" || p.Ext == ".mp4" {
				videoPosts = append(videoPosts, p)
			}
		}
		if len(videoPosts) == 0 {
			continue
		}

		post := videoPosts[rng.Intn(len(videoPosts))]
		replies := getRepliesTo(thread.Posts, post.No)

		return VideoResponse{
			URL: fmt.Sprintf("/proxy?url=%s%d%s", mediaBase, post.Tim, post.Ext),
			Filename:  post.Filename + post.Ext,
			ThreadID:  id,
			PostNo:    post.No,
			ThreadURL: fmt.Sprintf(threadLink, id, post.No),
			Replies:   replies,
		}, nil
	}

	return VideoResponse{}, fmt.Errorf("no videos found")
}

func videoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	video, err := randomVideo()
	if err != nil {
		http.Error(w, `{"error":"failed to fetch video"}`, http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(video)
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
    videoURL := r.URL.Query().Get("url")
    if videoURL == "" {
        http.Error(w, "missing url param", http.StatusBadRequest)
        return
    }

    // Only allow 4chan CDN URLs
    if !strings.HasPrefix(videoURL, "https://i.4cdn.org/") {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }

    resp, err := http.Get(videoURL)
    if err != nil {
        http.Error(w, "failed to fetch video", http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()

    // Forward relevant headers
    w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
    w.Header().Set("Content-Length", resp.Header.Get("Content-Length"))
    w.Header().Set("Accept-Ranges", "bytes")
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.WriteHeader(resp.StatusCode)
    io.Copy(w, resp.Body)
}

func main() {
	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	http.HandleFunc("/api/random-video", videoHandler)
	http.HandleFunc("/proxy", proxyHandler)
	http.Handle("/", http.FileServer(http.Dir("./static")))

	log.Printf("Server running at http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
