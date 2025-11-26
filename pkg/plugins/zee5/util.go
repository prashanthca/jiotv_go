package zee5

import (
    "bytes"
    "bufio"
    "compress/gzip"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "regexp"
    "strings"
    "github.com/google/uuid"
    "github.com/gofiber/fiber/v2"
    "github.com/jiotv-go/jiotv_go/v3/pkg/secureurl"
    "github.com/jiotv-go/jiotv_go/v3/pkg/utils"
)

const (
    USER_AGENT   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:145.0) Gecko/20100101 Firefox/145.0"
    playbackURL  = "https://spapi.zee5.com/singlePlayback/getDetails/secure"
)

// generateDDToken generates the 'x-dd-token' header value by Base64 encoding
// a JSON string of device capabilities.
func generateDDToken() (string, error) {
    data := map[string]interface{}{
        "schema_version": "1",
        "os_name": "N/A",
        "os_version": "N/A",
        "platform_name": "Chrome",
        "platform_version": "104",
        "device_name": "",
        "app_name": "Web",
        "app_version": "2.52.31",
        "player_capabilities": map[string]interface{}{
            "audio_channel": []string{"STEREO"},
            "video_codec":   []string{"H264"},
            "container":     []string{"MP4", "TS"},
            "package":       []string{"DASH", "HLS"},
            "resolution":    []string{"240p", "SD", "HD", "FHD"},
            "dynamic_range": []string{"SDR"},
        },
        "security_capabilities": map[string]interface{}{
            "encryption":              []string{"WIDEVINE_AES_CTR"},
            "widevine_security_level": []string{"L3"},
            "hdcp_version":            []string{"HDCP_V1", "HDCP_V2", "HDCP_V2_1", "HDCP_V2_2"},
        },
    }

    jsonBytes, err := json.Marshal(data)
    if err != nil {
        return "", fmt.Errorf("failed to marshal JSON: %w", err)
    }

    // Base64 encode the JSON bytes
    encoded := base64.StdEncoding.EncodeToString(jsonBytes)

    return encoded, nil
}

// generateGuestToken generates a version 4 (random) UUID string.
func generateGuestToken() string {
    return uuid.New().String()
}

// fetchPlatformToken GETs the ZEE5 homepage and extracts the embedded platformToken.
func fetchPlatformToken(userAgent string) (string, error) {
    client := &http.Client{}
    req, err := http.NewRequest("GET", "https://www.zee5.com/", nil)
    if err != nil {
        return "", fmt.Errorf("failed to create request: %w", err)
    }
    req.Header.Set("User-Agent", userAgent)
    req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
    req.Header.Set("Accept-Language", "en-US,en;q=0.9")

    resp, err := client.Do(req)
    if err != nil {
        return "", fmt.Errorf("GET zee5.com: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("GET zee5.com returned HTTP %d", resp.StatusCode)
    }

    var reader io.Reader = resp.Body
    if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
        gz, err := gzip.NewReader(resp.Body)
        if err != nil {
            return "", fmt.Errorf("gzip reader: %w", err)
        }
        defer gz.Close()
        reader = gz
    }

    bodyBytes, err := io.ReadAll(reader)
    if err != nil {
        return "", fmt.Errorf("reading zee5.com body: %w", err)
    }

    re := regexp.MustCompile(`"platformToken":"([^"]+)"`)
    matches := re.FindSubmatch(bodyBytes)
    if matches == nil {
        return "", fmt.Errorf("platformToken not found in zee5.com HTML")
    }
    return string(matches[1]), nil
}

type playbackRequest struct {
	XAccessToken  string `json:"x-access-token"`
	XZ5GuestToken string `json:"X-Z5-Guest-Token"`
	XDDToken      string `json:"x-dd-token"`
}

type playbackResponse struct {
	KeyOsDetails struct {
		VideoToken string `json:"video_token"`
	} `json:"keyOsDetails"`
}

// fetchVideoToken calls the singlePlayback API for a specific channel and returns the m3u8 URL.
func fetchVideoToken(channelID string) (string, error) {
	platformToken, err := fetchPlatformToken(USER_AGENT)
	if err != nil {
		return "", fmt.Errorf("platform token: %w", err)
	}

	guestToken := generateGuestToken()

	ddToken, err := generateDDToken()
	if err != nil {
		return "", fmt.Errorf("dd token: %w", err)
	}

	q := url.Values{}
	q.Set("channel_id", channelID)
	q.Set("device_id", guestToken)
	q.Set("platform_name", "desktop_web")
	q.Set("translation", "en")
	q.Set("user_language", "en")
	q.Set("country", "IN")
	q.Set("state", "KA")
	q.Set("app_version", "5.8.0")
	q.Set("user_type", "guest")
	q.Set("check_parental_control", "false")
	q.Set("ppid", guestToken)
	q.Set("version", "15")

	body, _ := json.Marshal(playbackRequest{
		XAccessToken:  platformToken,
		XZ5GuestToken: guestToken,
		XDDToken:      ddToken,
	})

	client := &http.Client{}
	req, err := http.NewRequest("POST", playbackURL+"?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", USER_AGENT)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://www.zee5.com")
	req.Header.Set("Referer", "https://www.zee5.com/")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST singlePlayback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("singlePlayback returned HTTP %d", resp.StatusCode)
	}

	var pr playbackResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if pr.KeyOsDetails.VideoToken == "" {
		return "", fmt.Errorf("video_token not found in API response")
	}
	return pr.KeyOsDetails.VideoToken, nil
}

func transformURL(relURLStr string, baseURL *url.URL, isMaster bool, prefix string) string {
	relURL, err := url.Parse(relURLStr)
	if err != nil {
		return relURLStr
	}

	absURL := baseURL.ResolveReference(relURL).String()
	coded_url, err := secureurl.EncryptURL(absURL)
	if err != nil {
		utils.Log.Println(err)
		return ""
	}
	path := relURL.Path
	if path == "" {
		path = relURL.String()
	}

	// Simple extension check
	isM3U8 := strings.Contains(path, ".m3u8")
	isSegment := strings.Contains(path, ".ts") || strings.Contains(path, ".mp4")
	segmentType := ""
	if strings.Contains(path, ".mp4") {
		segmentType = "mp4"
	} else {
		segmentType = "ts"
	}
	if isM3U8 {
		// Construct new URL
		newParams := url.Values{}
		
		newParams.Set("auth", coded_url)
		return fmt.Sprintf("%s/zee5/render/playlist.m3u8?%s", prefix, newParams.Encode())

	} else if isSegment && !isMaster {
		// Proxy segments only in Index handler
		newParams := url.Values{}
		newParams.Set("auth", coded_url)
		return fmt.Sprintf("%s/zee5/render/segment.%s?%s", prefix, segmentType, newParams.Encode())
	}

	// Fallback: use absolute URL
	return absURL
}

func fetchContent(targetURL string) ([]byte, http.Header, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("User-Agent", USER_AGENT)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	return body, resp.Header, err
}

// handlePlaylist contains the common logic for processing m3u8 playlists
func handlePlaylist(c *fiber.Ctx, isMaster bool, targetURLStr string, prefix string) {
	if targetURLStr == "" {
		c.Status(fiber.StatusBadRequest).SendString("missing url param")
		return
	}

	// Fetch content
	content, _, err := fetchContent(targetURLStr)
	if err != nil {
        c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("failed to fetch: %v", err))
		return
	}

	// Base URL for resolution
	baseURL, err := url.Parse(targetURLStr)
	if err != nil {
		c.Status(fiber.StatusBadRequest).SendString("invalid target url")
		return
	}

	// Process content
	var processedLines []string
	scanner := bufio.NewScanner(bytes.NewReader(content))
	
	// Regex for EXT-X-MEDIA URI
	reMediaURI := regexp.MustCompile(`URI="([^"]+)"`)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			processedLines = append(processedLines, line)
			continue
		}
		if strings.HasPrefix(trimmed, "#EXT-X-MAP") || strings.HasPrefix(trimmed, "#EXT-X-MEDIA") {
			// Handle URI inside EXT-X-MAP or EXT-X-MEDIA
			matches := reMediaURI.FindStringSubmatch(trimmed)
			if len(matches) > 1 {
				originalURI := matches[1]
				newURI := transformURL(originalURI, baseURL, isMaster, prefix)
				line = strings.Replace(line, originalURI, newURI, 1)
			}
			processedLines = append(processedLines, line)
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			processedLines = append(processedLines, line)
			continue
		}

		// It's a URI line
		newLine := transformURL(trimmed, baseURL, isMaster, prefix)
		processedLines = append(processedLines, newLine)
	}

	c.Set("Content-Type", "application/vnd.apple.mpegurl")
	c.Set("Access-Control-Allow-Origin", "*") // Good practice for proxy

	c.Send([]byte(strings.Join(processedLines, "\n")))
}

// ProxySegmentHandler handles the /segment.ts endpoint
func ProxySegmentHandler(c *fiber.Ctx) {
	targetURLStr := c.Query("auth")
	if targetURLStr == "" {
		c.Status(fiber.StatusBadRequest).SendString("missing auth param")
		return
	}

	coded_url, err := secureurl.DecryptURL(c.Query("auth"))
	if err != nil {
		c.Status(fiber.StatusBadRequest).SendString("invalid auth param")
		return
	}
	targetURLStr = coded_url

	content, respHeaders, err := fetchContent(targetURLStr)
	if err != nil {
		c.Status(fiber.StatusInternalServerError).SendString(fmt.Sprintf("failed to fetch: %v", err))
		return
	}

	// Copy headers
	if ct := respHeaders.Get("Content-Type"); ct != "" {
		c.Set("Content-Type", ct)
	}
	if cl := respHeaders.Get("Content-Length"); cl != "" {
		c.Set("Content-Length", cl)
	}
	c.Set("Access-Control-Allow-Origin", "*")

	c.Send(content)
}