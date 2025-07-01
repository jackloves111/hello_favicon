package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	_ "github.com/biessek/golang-ico"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/nfnt/resize"
)

type FaviconResponse struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	URL         string            `json:"url"`
	FaviconURL  string            `json:"faviconUrl"`
	Favicons    map[string]string `json:"favicons"` // 尺寸 -> base64编码
}

// APIResponse 简化的API响应结构
type APIResponse struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	FaviconURL  string `json:"faviconUrl"`
}

// 默认的HTTP客户端
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		TLSHandshakeTimeout: 5 * time.Second,
		DisableKeepAlives:   false,
		Proxy: func(req *http.Request) (*url.URL, error) {
			proxyURL := ""
			// 按优先级检测代理变量
			for _, env := range []string{"HTTPS_PROXY", "HTTP_PROXY"} {
				if proxy := os.Getenv(env); proxy != "" {
					proxyURL = proxy
				}
			}
			if proxyURL != "" {
				return url.Parse(proxyURL)
			}
			return nil, nil // 无代理
		},
	},
}

func main() {
	r := gin.Default()

	// 配置CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Origin", "Content-Type"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// 静态文件服务
	r.Static("/static", "./static")
	r.LoadHTMLGlob("templates/*")

	// 主页
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	// Favicon API
	r.POST("/api/favicon", getFavicon)
	// 简化的GET API，支持查询参数
	r.GET("/api", getWebsiteInfoByQuery)

	r.Run(":3000")
}

func getFavicon(c *gin.Context) {
	var requestData struct {
		URL string `json:"url" binding:"required"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL is required"})
		return
	}

	targetURL := requestData.URL
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	// 解析URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL"})
		return
	}

	// 创建带有User-Agent的请求
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	// 获取HTML
	resp, err := httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch website: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Website returned status code: %d", resp.StatusCode),
		})
		return
	}

	// 读取并解析HTML
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read HTML content"})
		return
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse HTML"})
		return
	}

	// 获取网站标题
	title := doc.Find("title").Text()
	if title == "" {
		title = parsedURL.Hostname()
	}

	// 获取网站描述
	description := ""
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		if name, _ := s.Attr("name"); strings.ToLower(name) == "description" {
			if desc, exists := s.Attr("content"); exists {
				description = desc
			}
		}
	})

	// 寻找所有可能的favicon URLs
	faviconURLs := findFavicons(doc, parsedURL)

	// 尝试获取并解码favicon
	var img image.Image
	var faviconURL string
	var success bool

	for _, url := range faviconURLs {
		img, success = fetchAndDecodeImage(url)
		if success {
			faviconURL = url
			break
		}
	}

	// 如果所有favicon获取都失败，生成一个基于域名的图标
	if !success {
		img = generateDomainIcon(parsedURL.Hostname())
		faviconURL = "generated"
	}

	// 生成不同尺寸的favicon
	sizes := []uint{16, 32, 64, 128, 256}
	favicons := make(map[string]string)

	for _, size := range sizes {
		resized := resize.Resize(size, size, img, resize.Bilinear)

		var buf bytes.Buffer
		if err := png.Encode(&buf, resized); err != nil {
			continue
		}

		// 转换为base64
		base64Str := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		favicons[fmt.Sprintf("%d", size)] = base64Str
	}

	// 返回结果
	c.JSON(http.StatusOK, FaviconResponse{
		Title:       title,
		Description: description,
		URL:         targetURL,
		FaviconURL:  faviconURL,
		Favicons:    favicons,
	})
}

// 查找所有可能的favicon URLs
func findFavicons(doc *goquery.Document, baseURL *url.URL) []string {
	faviconCandidates := []string{}

	// 检查各种link标签
	doc.Find("link").Each(func(i int, s *goquery.Selection) {
		rel, _ := s.Attr("rel")
		rel = strings.ToLower(rel)

		if rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon" ||
			rel == "apple-touch-icon-precomposed" || strings.Contains(rel, "icon") {
			if href, exists := s.Attr("href"); exists && href != "" {
				faviconCandidates = append(faviconCandidates, href)
			}
		}
	})

	// 添加常见的favicon路径
	commonPaths := []string{
		"/favicon.ico",
	}

	for _, path := range commonPaths {
		faviconCandidates = append(faviconCandidates, path)
	}

	// 转换所有相对路径为绝对路径
	absoluteURLs := []string{}
	for _, candidate := range faviconCandidates {
		absoluteURL := makeAbsoluteURL(candidate, baseURL)
		if absoluteURL != "" {
			absoluteURLs = append(absoluteURLs, absoluteURL)
		}
	}

	// 去重
	return removeDuplicates(absoluteURLs)
}

// 将相对URL转换为绝对URL
func makeAbsoluteURL(urlStr string, baseURL *url.URL) string {
	if urlStr == "" {
		return ""
	}
	candidateURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	if candidateURL.IsAbs() {
		return candidateURL.String()
	}
	absURL := baseURL.ResolveReference(candidateURL)
	return absURL.String()
}

// 去除重复URL
func removeDuplicates(urls []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, url := range urls {
		if !seen[url] {
			seen[url] = true
			result = append(result, url)
		}
	}

	return result
}

// fetchWithRetry 带重试机制的HTTP请求函数
func fetchWithRetry(targetURL string, maxRetries int, retryDelay time.Duration) (*http.Response, error) {
	var lastErr error
	
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 创建带有User-Agent的请求
		req, err := http.NewRequest("GET", targetURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %v", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		
		// 发送请求
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				fmt.Printf("请求失败 (尝试 %d/%d): %v，%v秒后重试...\n", attempt+1, maxRetries+1, err, retryDelay.Seconds())
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("所有重试都失败了: %v", err)
		}
		
		// 检查状态码 - 对所有非200状态码进行重试
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("服务器返回状态码: %d", resp.StatusCode)
			if attempt < maxRetries {
				fmt.Printf("收到 %d 状态码 (尝试 %d/%d)，%v秒后重试...\n", resp.StatusCode, attempt+1, maxRetries+1, retryDelay.Seconds())
				time.Sleep(retryDelay)
				continue
			}
			return nil, lastErr
		}
		
		// 请求成功
		return resp, nil
	}
	
	return nil, lastErr
}

// 获取并解码图像
func fetchAndDecodeImage(imageURL string) (image.Image, bool) {
	// 判断是否是 Base64 Data URL
	if strings.HasPrefix(imageURL, "data:image/") {
		// 按逗号分割 Data URL，提取 Base64 部分
		parts := strings.SplitN(imageURL, ",", 2)
		if len(parts) != 2 {
			fmt.Println("无效的 Data URL 格式")
			return nil, false
		}

		// 获取 Base64 数据
		base64Data := parts[1]

		// 解码 Base64
		imgData, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			fmt.Println("Base64 解码失败:", err)
			return nil, false
		}

		// 解码为图像
		img, _, err := image.Decode(bytes.NewReader(imgData))
		if err != nil {
			fmt.Println("图像解码失败:", err)
			return nil, false
		}
		return img, true
	}

	// 使用重试机制发送请求
	resp, err := fetchWithRetry(imageURL, 2, 1*time.Second)
	if err != nil {
		fmt.Printf("获取图像失败 %s: %v\n", imageURL, err)
		return nil, false
	}
	defer resp.Body.Close()

	// 读取响应体
	imgData, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("读取图像数据失败: %v\n", err)
		return nil, false
	}

	// 检查是否为 SVG
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "image/svg+xml") || strings.HasSuffix(strings.ToLower(imageURL), ".svg") {
		// 使用 rasterx 将 SVG 转换为光栅图像
		//svgImg, err := rasterx.SVGToImage(, 128, 128)
		svgImg, err := renderSVGToImage(imgData)
		if err != nil {
			fmt.Printf("SVG 解码失败: %v\n", err)
			return nil, false
		}
		return svgImg, true
	}

	// 处理ICO文件
	_, format, err := image.DecodeConfig(bytes.NewReader(imgData))
	if !(format == "png" || format == "jpeg" || format == "ico") {
		fmt.Printf("忽略非支持的格式: %v %v\n", err, format)
		return nil, false
	}
	// 尝试解码图像
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		fmt.Printf("解码图像失败: %v\n", err)
		return nil, false
	}

	return img, true
}

// 将SVG渲染为位图图像
func renderSVGToImage(data []byte) (image.Image, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// 设置渲染尺寸（示例使用256x256，根据需求调整）
	w, h := 256.0, 256.0
	if vb := icon.ViewBox; vb.W > 0 && vb.H > 0 {
		w, h = vb.W, vb.H
	}

	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	scanner := rasterx.NewScannerGV(int(w), int(h), img, img.Bounds())
	raster := rasterx.NewDasher(int(w), int(h), scanner)
	icon.Draw(raster, 1.0)
	return img, nil
}

// 生成基于域名的图标
func generateDomainIcon(domain string) image.Image {
	// 创建一个方形图像
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))

	// 生成背景颜色 (基于域名的哈希值)
	var hash uint32 = 0
	for i := 0; i < len(domain); i++ {
		hash = hash*31 + uint32(domain[i])
	}

	// 使用哈希值生成颜色
	r := uint8((hash >> 16) % 200)
	g := uint8((hash >> 8) % 200)
	b := uint8(hash % 200)

	// 确保背景色不会太暗
	r += 55
	g += 55
	b += 55

	bgColor := color.RGBA{r, g, b, 255}
	textColor := color.RGBA{255, 255, 255, 255}

	// 填充背景
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// 提取域名首字母并绘制
	var initial string = "?"
	parts := strings.Split(domain, ".")
	if len(parts) > 0 && len(parts[0]) > 0 {
		initial = strings.ToUpper(string(parts[0][0]))
	}

	// 在中心绘制一个圆形，作为首字母的背景
	centerX := img.Bounds().Dx() / 2
	centerY := img.Bounds().Dy() / 2
	radius := img.Bounds().Dx() / 4

	for y := centerY - radius; y <= centerY+radius; y++ {
		for x := centerX - radius; x <= centerX+radius; x++ {
			dx := x - centerX
			dy := y - centerY
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, textColor)
			}
		}
	}

	// 在圆形中心绘制简单的字母形状
	drawInitial(img, initial, centerX, centerY, bgColor)

	return img
}

// 简单地绘制字母的点阵表示
func drawInitial(img *image.RGBA, initial string, centerX, centerY int, color color.RGBA) {
	// 定义字母大小
	size := 20

	// 根据不同字母绘制不同的形状
	switch initial {
	case "A":
		// 绘制A形状
		for i := -size / 2; i <= size/2; i++ {
			for j := -size / 2; j <= size/2; j++ {
				// 简单的A形状：两条斜线和一条横线
				if (i == j*2 || i == -j*2) || (j == 0 && i >= -size/4 && i <= size/4) {
					img.Set(centerX+i, centerY+j, color)
				}
			}
		}
	case "B":
		// 绘制B形状
		for i := -size / 2; i <= size/2; i++ {
			for j := -size / 2; j <= size/2; j++ {
				if i == -size/2 || j == -size/2 || j == size/2 || j == 0 ||
					(i == size/2 && (j > -size/2 && j < 0 || j > 0 && j < size/2)) {
					img.Set(centerX+i, centerY+j, color)
				}
			}
		}
	// 可以添加更多字母的绘制逻辑
	default:
		// 默认绘制一个简单的十字
		for i := -size / 2; i <= size/2; i++ {
			img.Set(centerX+i, centerY, color)
			img.Set(centerX, centerY+i, color)
		}
	}
}

// getWebsiteInfoByQuery 处理GET请求，通过查询参数获取网站信息
func getWebsiteInfoByQuery(c *gin.Context) {
	// 从查询参数获取URL，只支持空参数名
	targetURL := c.Query("")
	
	if targetURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL parameter is required"})
		return
	}

	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "https://" + targetURL
	}

	// 解析URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL"})
		return
	}

	// 创建带有User-Agent的请求
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	// 获取HTML
	resp, err := httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch website: %v", err)})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Website returned status code: %d", resp.StatusCode),
		})
		return
	}

	// 读取并解析HTML
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read HTML content"})
		return
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse HTML"})
		return
	}

	// 获取网站标题
	title := doc.Find("title").Text()
	if title == "" {
		title = parsedURL.Hostname()
	}

	// 获取网站描述
	description := ""
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		if name, _ := s.Attr("name"); strings.ToLower(name) == "description" {
			if desc, exists := s.Attr("content"); exists {
				description = desc
			}
		}
	})

	// 寻找favicon URL
	faviconURLs := findFavicons(doc, parsedURL)
	faviconURL := ""
	
	if len(faviconURLs) > 0 {
		// 尝试获取第一个有效的favicon
		for _, url := range faviconURLs {
			_, success := fetchAndDecodeImage(url)
			if success {
				faviconURL = url
				break
			}
		}
	}
	
	// 如果没有找到有效的favicon，使用生成的URL
	if faviconURL == "" {
		faviconURL = fmt.Sprintf("http://127.0.0.1:3000/static/favicon.svg")
	}

	// 返回简化的JSON格式
	c.JSON(http.StatusOK, APIResponse{
		Title:       title,
		Description: description,
		URL:         targetURL,
		FaviconURL:  faviconURL,
	})
}
