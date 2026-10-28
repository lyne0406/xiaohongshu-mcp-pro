# ---- build stage ----
FROM golang:1.24 AS builder

WORKDIR /src
# 配置 Go 模块代理为国内源
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/app .

# ---- run stage ----
FROM ubuntu:22.04

# 设置时区
ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

# 1. 先安装必要工具，然后配置阿里云镜像源
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates wget gnupg

# 2. 添加 Google Chrome APT 源并安装 Chrome（更稳定的无头浏览器）
RUN wget -q -O - https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/googlechrome-linux-keyring.gpg && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/googlechrome-linux-keyring.gpg] http://dl.google.com/linux/chrome/deb/ stable main" >> /etc/apt/sources.list.d/google-chrome.list

# 3. 安装 Google Chrome + 依赖（无头模式运行 rod）
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    fonts-liberation \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libc6 \
    libcairo2 \
    libcups2 \
    libdbus-1-3 \
    libexpat1 \
    libfontconfig1 \
    libgbm1 \
    libgcc1 \
    libglib2.0-0 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    libstdc++6 \
    libx11-6 \
    libx11-xcb1 \
    libxcb1 \
    libxcomposite1 \
    libxcursor1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxi6 \
    libxrandr2 \
    libxrender1 \
    libxss1 \
    libxtst6 \
    lsb-release \
    wget \
    xdg-utils \
    google-chrome-stable \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/app .

# Railway 独立持久卷 /app/data: 保留小号的 Chrome Profile 与 Cookies
ENV COOKIES_PATH=/app/data/cookies.json
ENV ACCOUNTS_PATH=/app/data/accounts.json
ENV HOME=/app/data/home
ENV XDG_CONFIG_HOME=/app/data/config
RUN mkdir -p /app/data/home /app/data/config && chmod -R 777 /app/data

# 4. 创建共享目录并设置权限
RUN mkdir -p /app/images && \
    chmod 777 /app/images

# 5. 设置默认 Chrome 路径（rod 会用）
ENV ROD_BROWSER_BIN=/usr/bin/google-chrome

EXPOSE 18060

CMD ["./app"]

