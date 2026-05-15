package zee5

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jiotv-go/jiotv_go/v3/pkg/television"
	"embed"
	"encoding/json"
	"strings"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jiotv-go/jiotv_go/v3/pkg/secureurl"
	internalUtils "github.com/jiotv-go/jiotv_go/v3/internal/utils"
	"time"
)

var urlCache *expirable.LRU[string, string]

func init() {
	urlCache = expirable.NewLRU[string, string](100, nil, time.Second*3600)
}
type ChannelItem struct {
    ID   string `json:"id"`
    Name string `json:"name"`
    URL  string `json:"url"`
    Logo string `json:"logo"`
	Language int `json:"language"`
	Genre int `json:"genre"`
	Slug string `json:"slug"`
}

type DataFile struct {
    Title string        `json:"title"`
    Data  []ChannelItem `json:"data"`
}

func readDataFile() (*DataFile, error) {
    b, err := dataFile.ReadFile("data.json")
    if err != nil {
        return nil, err
    }
    var d DataFile
    if err := json.Unmarshal(b, &d); err != nil {
        return nil, err
    }
    return &d, nil
}

//go:embed data.json
var dataFile embed.FS
func LiveHandler(c *fiber.Ctx) error {
	id := c.Params("id")
	id = strings.Replace(id, ".m3u8", "", 1)

	videoURL, found := urlCache.Get(id)
	if !found {
		var err error
		videoURL, err = fetchVideoToken(id)
		if err != nil {
			c.Status(fiber.StatusInternalServerError).SendString(err.Error())
			return err
		}
		urlCache.Add(id, videoURL)
	}

	hostURL := strings.ToLower(c.Protocol()) + "://" + c.Hostname()
	handlePlaylist(c, true, videoURL, hostURL)
	return nil
}

func RenderHandler(c *fiber.Ctx) error {
	hostURL := strings.ToLower(c.Protocol()) + "://" + c.Hostname()
	coded_url, err := secureurl.DecryptURL(c.Query("auth"))
	if err != nil {
		return err
	}
	handlePlaylist(c, false, coded_url, hostURL)
	return nil
}

func RenderTSChunkHandler(c *fiber.Ctx) error {
	ProxySegmentHandler(c)
	return nil
}

func RenderMP4ChunkHandler(c *fiber.Ctx) error {
	ProxySegmentHandler(c)
	return nil
}

func RegisterRoutes(app *fiber.App) {
	app.Get("/zee5/:id", LiveHandler)
	app.Get("/zee5/play/:id", PlayHandler)
	app.Get("/zee5/player/:id", PlayerHandler)
	app.Get("/zee5/render/playlist.m3u8", RenderHandler)
	app.Get("/zee5/render/segment.ts", RenderTSChunkHandler)
	app.Get("/zee5/render/segment.mp4", RenderMP4ChunkHandler)
}

func PlayHandler(c *fiber.Ctx) error {
	id := c.Params("id")
	player_url := "/zee5/player/" + id

	internalUtils.SetCacheHeader(c, 3600)
	return c.Render("views/play", fiber.Map{
		"Title":      "Zee5",
		"player_url": player_url,
		"ChannelID":  id,
	})
}

func PlayerHandler(c *fiber.Ctx) error {
	id := c.Params("id")
	play_url := "/zee5/"+id+".m3u8"
	internalUtils.SetCacheHeader(c, 3600)
	return c.Render("views/player_hls", fiber.Map{
		"play_url": play_url,
	})
}

func GetChannels() []television.Channel {
	data, err := readDataFile()
	channels := []television.Channel{}
	if err != nil {
		return nil
	}
	for _, channelItem := range data.Data {
		channels = append(channels, television.Channel{
			ID:       channelItem.ID,
			Name:     channelItem.Name,
			URL:      "zee5/" + channelItem.ID,
			LogoURL:  channelItem.Logo,
			Category: channelItem.Genre,
			Language: channelItem.Language,
			IsHD:     false,
			IsCustom: true,
			PluginID: "zee5",
			IsCatchupAvailable: false,
		})
	}
	return channels
}