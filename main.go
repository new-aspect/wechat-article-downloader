package main

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/new-aspect/wechat-article-downloader/domain"

	"github.com/chromedp/chromedp"
)

//go:embed web/index.html
var content embed.FS

// 全局变量，用于临时接收前端传来的 URL
var targetUrl string

func main() {
	// 1. 设置 Web 路由
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/start", handleStart)

	// 2. 启动浏览器 (核心体验优化)
	// 使用 goroutine 稍微延迟一点，确保服务器先启动
	go func() {
		time.Sleep(500 * time.Millisecond) // 等 0.5 秒
		openBrowser("http://localhost:12345")
	}()

	fmt.Println("=======================================")
	fmt.Println("   公众号下载器 Web版已启动   ")
	fmt.Println("   请在浏览器访问: http://localhost:12345   ")
	fmt.Println("=======================================")

	// 3. 启动 HTTP 服务 (阻塞运行)
	// 监听 0.0.0.0 允许局域网访问，但在朋友电脑上通常就是 localhost
	err := http.ListenAndServe(":12345", nil)
	if err != nil {
		log.Fatal("服务器启动失败: ", err)
	}
}

// 首页：直接返回内嵌的 HTML
func handleIndex(w http.ResponseWriter, r *http.Request) {
	page, _ := content.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

// API：接收前端的开始指令
// API：接收前端的开始指令 (SSE 模式)
func handleStart(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "URL cannot be empty", http.StatusBadRequest)
		return
	}

	// 1. 设置 SSE 必要的 HTTP 头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 2. 立即发送一个连接成功的信号
	SSELog(w, "🚀 后台服务已连接，准备开始任务...")

	// 3. 调用业务逻辑 (Processor)
	// 注意：这里是同步调用，直到 ProcessTask 跑完，这个 HTTP 请求才会结束
	// 所有的日志都会通过 w 实时推送到前端
	ProcessTask(w, url)
}

// -------------------------------------------------
// 辅助工具：自动打开浏览器 (兼容 Mac 和 Windows)
// -------------------------------------------------

func openBrowser(url string) {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		// Windows 下使用 cmd /c start
		err = exec.Command("cmd", "/c", "start", url).Start()
	case "darwin":
		// Mac 下使用 open
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}

	if err != nil {
		log.Printf("尝试自动打开浏览器失败 (请手动访问 %s): %v\n", url, err)
	}
}

func runSpider() {
	// 🔗 这里填那个包含很多链接的“目录页” URL
	// (就是你刚才复制那大段文字的来源页面)
	indexUrl := "https://mp.weixin.qq.com/s/EEq12wnalxykZQjt2ozCaQ"

	fmt.Println("🕷️ 正在启动蜘蛛，准备爬取目录页...")

	// 1. 启动浏览器
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // 有头模式，看着它跑
		chromedp.WindowSize(1200, 900),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// 设置超时
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// 2. 抓取页面所有的 href
	var allLinks []string
	err := chromedp.Run(ctx,
		chromedp.Navigate(indexUrl),
		chromedp.WaitVisible("#js_content", chromedp.ByID), // 等正文出来

		// 【核心黑科技】 直接在浏览器里执行一段 JS，把所有 a 标签的 href 拿出来
		// 这比去解析 HTML 字符串要准得多，因为浏览器已经帮你处理好相对路径了
		chromedp.Evaluate(`Array.from(document.querySelectorAll('#js_content a')).map(a => a.href)`, &allLinks),
	)

	if err != nil {
		log.Fatal("❌ 抓取失败:", err)
	}

	fmt.Printf("🔍 页面上一共找到了 %d 个链接，正在过滤...\n", len(allLinks))

	// 3. 过滤和去重 (Filter & Deduplicate)
	validUrls := make([]string, 0)
	seen := make(map[string]bool)

	for _, link := range allLinks {
		// 规则A: 必须是微信文章链接
		if !strings.Contains(link, "mp.weixin.qq.com/s") {
			continue
		}
		// 规则B: 去重
		if seen[link] {
			continue
		}
		seen[link] = true
		validUrls = append(validUrls, link)
	}

	fmt.Printf("✨ 提取到 %d 个有效文章链接！\n", len(validUrls))

	// 4. 写入 urls.txt
	saveToUrlsTxt(validUrls)
}

func saveToUrlsTxt(urls []string) {
	// O_APPEND 表示追加模式，不会覆盖你原有的
	// 如果你想覆盖，就把 os.O_APPEND 去掉，换成 os.O_TRUNC
	f, err := os.OpenFile("urls.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	for _, url := range urls {
		if _, err := f.WriteString(url + "\n"); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("💾 已自动保存到 urls.txt，快去运行你的下载器吧！")
}

func runDownloader() {
	// --- 1. 读取 urls.txt ---
	urls, err := readLines("urls.txt")
	if err != nil {
		log.Fatal("没找到 urls.txt，请先创建一个！")
	}
	fmt.Printf("📋 发现 %d 个待处理链接...\n", len(urls))

	// --- 2. 启动浏览器 (只启动一次，效率高) ---
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // 显示浏览器，让你看着爽
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1200, 900),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	// 创建【浏览器层】上下文 (Browser Context)
	// 这个 ctx 代表整个浏览器窗口，不要在循环里 cancel 它
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// --- 3. 开始循环 (Batch Processing) ---
	// ---------------------------------------------------------
	// 👇 重点改动在这里：循环内部逻辑
	// ---------------------------------------------------------
	for i, url := range urls {
		if strings.TrimSpace(url) == "" {
			continue
		}
		fmt.Printf("\n[%d/%d] 正在抓取: %s\n", i+1, len(urls), url)

		// 1. 【隔离策略】为当前 URL 创建一个新的 Tab
		// 基于 browserCtx 创建一个新的 tabCtx
		tabCtx, cancelTab := chromedp.NewContext(browserCtx)

		// 2. 【延长时间】把超时从 30s 改到 60s，给微信多点时间
		timeoutCtx, cancelTimeout := context.WithTimeout(tabCtx, 60*time.Second)

		var htmlContent string
		// 注意：这里 Run 使用的是 timeoutCtx (它是 tabCtx 的子集)
		err := chromedp.Run(timeoutCtx,
			chromedp.Navigate(url),
			chromedp.WaitVisible("#js_content", chromedp.ByID),
			chromedp.OuterHTML("html", &htmlContent),
		)

		// 3. 【资源回收】
		// 不管成功失败，都要先取消超时，再关闭 Tab
		cancelTimeout()
		cancelTab() // <--- 关键！这一步会关闭刚才打开的标签页

		// 错误处理
		if err != nil {
			// 区分一下是超时还是其他错误
			if err == context.DeadlineExceeded {
				fmt.Printf("⏳ 超时了 (60秒都没加载完): %s\n", url)
			} else {
				fmt.Printf("❌ 失败 (跳过): %v\n", err)
			}
			continue
		}

		// ... 后面的 Parse 和 Save 逻辑不变 ...
		article, err := domain.ConvertHtmlToArticle(htmlContent)
		if err != nil {
			fmt.Printf("⚠️ 解析失败: %v\n", err)
			continue
		}

		fileName := fmt.Sprintf("%s.md", sanitizeFilename(article.Title))
		content := fmt.Sprintf("# %s\n\n> 作者: %s\n> 原文: %s\n\n%s",
			article.Title, article.Author, url, article.Content)
		_ = os.WriteFile(fileName, []byte(content), 0644)
		fmt.Printf("✅ 已保存: %s\n", fileName)

		// 稍微休息一下，模拟人类阅读
		time.Sleep(2 * time.Second)
	}

	fmt.Println("\n🎉 全部搞定！")
}

// 辅助函数：读取文件行
func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// 辅助函数：清理文件名
//func sanitizeFilename(name string) string {
//	// Windows 非法字符：< > : " / \ | ? *
//	invalidChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
//	for _, char := range invalidChars {
//		name = strings.ReplaceAll(name, char, "-")
//	}
//	// 移除换行符等控制字符
//	name = strings.ReplaceAll(name, "\n", "")
//	name = strings.ReplaceAll(name, "\r", "")
//	return strings.TrimSpace(name)
//}
