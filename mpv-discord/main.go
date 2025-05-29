package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tnychn/mpv-discord/discordrpc"
	"github.com/tnychn/mpv-discord/mpvrpc"
)

var (
	client       *mpvrpc.Client
	presence     *discordrpc.Presence
	discordToken string
	tinyToken    string
)
var urls = map[string]string{}

func init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Lmsgprefix)

	client = mpvrpc.NewClient()
	presence = discordrpc.NewPresence(os.Args[2])
	discordToken = os.Args[3]
	tinyToken = os.Args[4]
}

var currTime int64 = time.Now().Local().UnixMilli()

func refreshCurrTime() {
	currTime = time.Now().Local().UnixMilli()
}

func getAppId() (appId string, err error) {
	if discordToken == "" {
		return "", errors.New("discord token is not provided")
	}
	req, _ := http.NewRequest("GET", "https://discord.com/api/v10/applications/@me", nil)
	req.Header.Set("Authorization", "Bot "+discordToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var appInfo struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&appInfo); err != nil {
		return "", err
	}
	return appInfo.ID, nil
}

func uploadImage(coverPath string) (url string, err error) {
	appId, err := getAppId()
	if err != nil {
		return "", err
	}
	file, err := os.Open(coverPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "cover.png")
	io.Copy(part, file)
	writer.Close()

	req, _ := http.NewRequest("POST", fmt.Sprintf("https://discord.com/api/v10/applications/%s/attachment", appId), body)
	req.Header.Set("Authorization", "Bot "+discordToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Attachment struct {
			URL string `json:"url"`
		} `json:"attachment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Attachment.URL, nil
}

func getTinyURL(url string) (tinyURL string, err error) {
	if tinyToken == "" {
		return "", errors.New("TinyURL API token is not provided")
	}

	payload := map[string]string{"url": url}
	jsonData, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", "https://api.tinyurl.com/create?api_token="+tinyToken, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			TinyURL string `json:"tiny_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Data.TinyURL, nil
}

func getCoverPath(path string) (coverPath string, err error) {
	coverRegexp := regexp.MustCompile(`(?i)^cover\.(jpe?g|png)$`)
	if files, err := os.ReadDir(path); err == nil {
		for _, file := range files {
			if coverRegexp.MatchString(file.Name()) {
				return path + "/" + file.Name(), nil
			}
		}
	}
	return "", fmt.Errorf("no cover image found in %s", path)
}

func getAlbumUrl(path string) (tinyUrl string, err error) {
	coverPath, err := getCoverPath(path)
	if err != nil {
		return "", err
	}

	if existsTinyUrl, ok := urls[coverPath]; ok {
		return existsTinyUrl, nil
	}

	if discordToken == "" || tinyToken == "" {
		return "", errors.New("discord token and tinyurl token are both required for this functionality")
	}
	// Upload the image and get the URL
	url, err := uploadImage(coverPath)
	if err != nil {
		return "", err
	}

	tinyUrl, err = getTinyURL(url)
	if err != nil {
		return "", err
	}

	urls[coverPath] = tinyUrl
	return tinyUrl, nil
}

func getActivity() (activity discordrpc.Activity, err error) {
	getProperty := func(key string) (prop interface{}) {
		prop, err = client.GetProperty(key)
		return
	}
	getPropertyString := func(key string) (prop string) {
		prop, err = client.GetPropertyString(key)
		return
	}

	// Large Image
	path := getPropertyString("path")
	path = path[0:strings.LastIndex(path, "/")]
	activity.LargeImageKey, err = getAlbumUrl(path)
	if err != nil {
		activity.LargeImageKey = "mpv"
		// 	log.Println("Error getting album cover:", err)
		// } else {
		// 	log.Println("Album cover URL:", activity.LargeImageKey)
	}

	activity.LargeImageText = "mpv"
	if version := getPropertyString("mpv-version"); version != "" {
		activity.LargeImageText += " " + version[4:]
	}

	// Details
	title := getPropertyString("media-title")
	fileFormat := getPropertyString("file-format")
	metaArtist := getProperty("metadata/by-key/Artist")
	metaAlbum := getProperty("metadata/by-key/Album")

	activity.Name = title
	activity.Details = title
	activity.Type = 2 // Default to Listening to
	// State
	if metaArtist != nil {
		// activity.State += " by " + metaArtist.(string)
		activity.State = metaArtist.(string)
	}
	if metaAlbum != nil {
		// activity.State += " on " + metaAlbum.(string)
		activity.LargeImageText = metaAlbum.(string) + " - " + activity.LargeImageText
	}

	if activity.State == "" {
		if aid, ok := getProperty("aid").(string); !ok || aid != "false" {
			activity.Type = 2
			activity.State += "Audio"
		}
		activity.State += "/"
		if vid, ok := getProperty("vid").(string); !ok || vid != "false" {
			activity.State += "Video"
			activity.Type = 3
		}
		activity.State += (": " + fileFormat)
	}

	// Small Image
	buffering := getProperty("paused-for-cache")
	pausing := getProperty("pause")
	loopingFile := getPropertyString("loop-file")
	loopingPlaylist := getPropertyString("loop-playlist")
	if buffering != nil && buffering.(bool) {
		activity.SmallImageKey = "buffer"
		activity.SmallImageText = "Buffering"
	} else if pausing != nil && pausing.(bool) {
		activity.SmallImageKey = "pause"
		activity.SmallImageText = "Paused"
	} else if loopingFile != "no" || loopingPlaylist != "no" {
		activity.SmallImageKey = "loop"
		activity.SmallImageText = "Looping"
	} else {
		activity.SmallImageKey = "play"
		activity.SmallImageText = "Playing"
	}
	if percentage := getProperty("percent-pos"); percentage != nil {
		activity.SmallImageText += fmt.Sprintf(" (%d%%)", int(percentage.(float64)))
	}
	if pcount := getProperty("playlist-count"); pcount != nil && int(pcount.(float64)) > 1 {
		if ppos := getProperty("playlist-pos-1"); ppos != nil {
			activity.SmallImageText += fmt.Sprintf(" [%d/%d]", int(ppos.(float64)), int(pcount.(float64)))
		}
	}

	// Timestamps
	_duration := getProperty("duration")
	durationMillis := int64(_duration.(float64))
	_timePos := getProperty("time-pos")
	timePosMills := int64(_timePos.(float64))

	startTimePos := currTime - (timePosMills * 1000)
	duration := startTimePos + (durationMillis * 1000)

	if pausing != nil && !pausing.(bool) {
		activity.Timestamps = &discordrpc.ActivityTimestamps{
			Start: startTimePos,
			End:   duration,
		}
		refreshCurrTime()
	}
	return
}

func openClient() {
	if err := client.Open(os.Args[1]); err != nil {
		log.Fatalln(err)
	}
	log.Println("(mpv-ipc): connected")
}

func openPresence() {
	// try until success
	for range time.Tick(500 * time.Millisecond) {
		if client.IsClosed() {
			return // stop trying when mpv shuts down
		}
		if err := presence.Open(); err == nil {
			break // break when successfully opened
		}
	}
	log.Println("(discord-ipc): connected")
}

func main() {
	defer func() {
		if !client.IsClosed() {
			if err := client.Close(); err != nil {
				log.Fatalln(err)
			}
			log.Println("(mpv-ipc): disconnected")
		}
		if !presence.IsClosed() {
			if err := presence.Close(); err != nil {
				log.Fatalln(err)
			}
			log.Println("(discord-ipc): disconnected")
		}
	}()

	openClient()
	go openPresence()

	for range time.Tick(time.Second) {
		activity, err := getActivity()
		// log.Printf("(discord-ipc): activity: %+v\n", activity)
		if err != nil {
			if errors.Is(err, syscall.EPIPE) {
				break
			} else if !errors.Is(err, io.EOF) {
				log.Println(err)
				continue
			}
		}
		if !presence.IsClosed() {
			go func() {
				if err = presence.Update(activity); err != nil {
					if errors.Is(err, syscall.EPIPE) {
						// close it before retrying
						if err = presence.Close(); err != nil {
							log.Fatalln(err)
						}
						log.Println("(discord-ipc): reconnecting...")
						go openPresence()
					} else if !errors.Is(err, io.EOF) {
						log.Println(err)
					}
				}
			}()
		}
	}
}
