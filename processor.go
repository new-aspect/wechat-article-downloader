package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/new-aspect/wechat-article-downloader/domain"
)

// SSELog 负责向前端推送日志
func SSELog(w http.ResponseWriter, msg string) {
	// 格式必须是 "data: 消息内容\n\n"
	fmt.Fprintf(w, "data: %s\n\n", msg)
	w.(http.Flusher).Flush() // 立即推送到前端，不缓存
}

// ProcessTask 是核心任务入口
// w: 用于发送 SSE 日志
// input: 用户粘贴的字符串（可能是目录链接，也可能是多个文章链接）
func ProcessTask(w http.ResponseWriter, input string) {
	// 1. 分析输入类型
	input = strings.TrimSpace(input)
	var urls []string

	// 如果包含空格，或者看起来像多个链接 -> 直接当作文章列表
	if strings.Contains(input, " ") || strings.Contains(input, "\n") {
		// 简单的按空格或换行分割
		parts := strings.Fields(input) // 自动处理空格、换行
		for _, p := range parts {
			if strings.Contains(p, "mp.weixin.qq.com") {
				urls = append(urls, p)
			}
		}
		SSELog(w, fmt.Sprintf("🔍 识别模式：直接下载模式 (检测到 %d 个链接)", len(urls)))
	} else {
		// 单个链接 -> 可能是目录页，也可能是单篇文章
		SSELog(w, "🔍 识别模式：爬虫模式 (正在解析目录...)")
		urls = fetchLinksFromCatalog(w, input)
	}

	if len(urls) == 0 {
		SSELog(w, "❌ 未找到有效的微信文章链接！")
		return
	}

	// 2. 开始批量下载
	runBatchDownload(w, urls)

	SSELog(w, "🎉 全部任务处理完成！请查看 output 文件夹。")
}

// ---------------------------------------------------------
// 内部逻辑：爬取目录页 (复用你之前的逻辑)
// ---------------------------------------------------------
func fetchLinksFromCatalog(w http.ResponseWriter, indexUrl string) []string {
	// 1. 启动浏览器
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.WindowSize(1200, 900),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 40*time.Second) // 40s 超时
	defer cancel()

	SSELog(w, "🕷️ 正在打开浏览器抓取目录...")

	var allLinks []string
	err := chromedp.Run(ctx,
		chromedp.Navigate(indexUrl),
		chromedp.WaitVisible("#js_content", chromedp.ByID), // 等正文
		chromedp.Evaluate(`Array.from(document.querySelectorAll('#js_content a')).map(a => a.href)`, &allLinks),
	)

	if err != nil {
		SSELog(w, fmt.Sprintf("❌ 抓取目录失败: %v", err))
		return nil
	}

	// 过滤
	var validUrls []string
	seen := make(map[string]bool)
	for _, link := range allLinks {
		if strings.Contains(link, "mp.weixin.qq.com/s") && !seen[link] {
			seen[link] = true
			validUrls = append(validUrls, link)
		}
	}
	SSELog(w, fmt.Sprintf("✅ 目录解析成功，发现 %d 篇文章", len(validUrls)))
	return validUrls
}

// ---------------------------------------------------------
// 内部逻辑：批量下载 (复用 + 增强稳定性)
// ---------------------------------------------------------
func runBatchDownload(w http.ResponseWriter, urls []string) {
	// 准备输出目录
	outputDir := "output"
	_ = os.MkdirAll(outputDir, 0755)

	// 启动浏览器上下文 (只启动一次)
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.WindowSize(1200, 900),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	for i, url := range urls {
		SSELog(w, fmt.Sprintf("⏳ [%d/%d] 正在下载: %s", i+1, len(urls), url))

		// 为每个标签页创建带超时的上下文
		tabCtx, cancelTab := chromedp.NewContext(ctx)
		timeoutCtx, cancelTimeout := context.WithTimeout(tabCtx, 60*time.Second)

		var htmlContent string
		err := chromedp.Run(timeoutCtx,
			chromedp.Navigate(url),
			chromedp.WaitVisible("#js_content", chromedp.ByID),
			chromedp.OuterHTML("html", &htmlContent),
		)

		cancelTimeout()
		cancelTab() // 关闭标签页

		if err != nil {
			SSELog(w, fmt.Sprintf("⚠️ 下载失败 (跳过): %v", err))
			continue
		}

		// 解析
		article, err := domain.ConvertHtmlToArticle(htmlContent)
		if err != nil {
			SSELog(w, fmt.Sprintf("⚠️ 解析失败: %v", err))
			continue
		}

		// 【关键】文件名清洗 (Windows 兼容)
		safeTitle := sanitizeFilename(article.Title)
		// 【关键】路径拼接 (Windows 兼容)
		filename := filepath.Join(outputDir, safeTitle+".md")

		content := fmt.Sprintf("# %s\n\n> 作者: %s\n> 原文: %s\n\n%s",
			article.Title, article.Author, url, article.Content)

		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			SSELog(w, fmt.Sprintf("❌ 保存文件失败: %v", err))
		} else {
			SSELog(w, fmt.Sprintf("✅ 已保存: %s", safeTitle))
		}

		// 稍微休息，防封
		time.Sleep(1 * time.Second)
	}
}

// sanitizeFilename 暴力清洗文件名，适配 Windows
func sanitizeFilename(name string) string {
	// 替换 Windows 非法字符
	invalidChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range invalidChars {
		name = strings.ReplaceAll(name, char, "-")
	}
	// 替换换行符
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\r", "")
	return strings.TrimSpace(name)
}
