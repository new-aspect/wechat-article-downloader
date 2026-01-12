package domain

import (
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
)

// Article 代表一篇解析后的文章
type Article struct {
	Title   string
	Author  string
	Content string // Markdown content
	Date    string
}

// ConvertHtmlToArticle 纯业务逻辑：HTML -> Article对象
func ConvertHtmlToArticle(htmlContent string) (*Article, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, err
	}

	// 1. 提取元数据 (微信公众号的特定 class)
	title := strings.TrimSpace(doc.Find("#activity-name").Text())
	author := strings.TrimSpace(doc.Find("#js_name").Text())
	// 简单的日期提取，实际可能需要从 script 里的 var 提取
	date := "2026-01-09"

	// 2. 清洗 HTML (移除广告、无用标签)
	// 这里可以运用很多“策略模式”，针对不同公众号做清洗
	doc.Find("script").Remove()
	doc.Find("style").Remove()

	// 3. 转换为 Markdown
	converter := md.NewConverter("", true, nil)
	markdown := converter.Convert(doc.Selection)
	//if err != nil {
	//	return nil, err
	//}

	return &Article{
		Title:   title,
		Author:  author,
		Content: markdown,
		Date:    date,
	}, nil
}
