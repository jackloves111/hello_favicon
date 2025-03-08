# 网站Favicon获取与转换工具

网站Favicon获取工具 - 高效抓取与转换网站图标，支持自动识别favicon.ico、PNG、SVG等多种格式。快速获取网站标题、描述及兼容性强的favicon图标，适配所有移动设备和浏览器。

## 功能特点

- 高性能
- 兼容性好
- 安装简单

## 安装说明

提前安装好docker、docker-compose命令环境

1. 拉取代码
```bash
https://github.com/luler/hello_favicon.git
```
2. 启动运行

```bash
docker-compose up -d
```

3. 访问页面：http://127.0.0.1:2025

## 使用说明

1. 在线页面生成
2. 提供接口获取：`POST` /api/favicon

* 请求参数：
```json
{
    "url": "http://127.0.0.1:8080/"
}
```
* 接口返回：
```json
{
    "title": "网站Favicon获取与转换工具",
    "description": "网站Favicon获取工具 - 高效抓取与转换网站图标，支持自动识别favicon.ico、PNG、SVG等多种格式。快速获取网站标题、描述及兼容性强的favicon图标，适配所有移动设备和浏览器。",
    "url": "http://127.0.0.1:8080/",
    "faviconUrl": "http://127.0.0.1:8080/static/favicon.svg",
    "favicons": {
        "16": "data:image/png;base64,iVBORw0KG",
        "32": "data:image/png;base64,iVBORw0KG",
        "64": "data:image/png;base64,iVBORw0KG",
        "128": "data:image/png;base64,iVBORw0KG",
        "256": "data:image/png;base64,iVBORw0KG"
    }
}
```