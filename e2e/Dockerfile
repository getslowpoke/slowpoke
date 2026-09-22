FROM node:22-alpine
RUN apk add --no-cache chromium font-noto-emoji ttf-dejavu
ENV PUPPETEER_SKIP_DOWNLOAD=1 CHROME_PATH=/usr/bin/chromium-browser
WORKDIR /e2e
RUN npm init -y >/dev/null && npm install puppeteer-core@23 >/dev/null
