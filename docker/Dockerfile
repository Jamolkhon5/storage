FROM golang:1.23.2-alpine

WORKDIR /app

# Установка необходимых пакетов
RUN apk add --no-cache \
    postgresql-client \
    curl \
    make \
    gcc \
    musl-dev \
    vips-dev \
    pkgconf \
    build-base \
    poppler-utils \
    libreoffice \
    libreoffice-base \
    libreoffice-lang-ru \
    libreoffice-writer \
    libreoffice-calc \
    ttf-dejavu \
    ttf-liberation \
    msttcorefonts-installer \
    fontconfig \
    ffmpeg

# Обновление шрифтов
RUN update-ms-fonts && fc-cache -f

# Создаем необходимые директории и устанавливаем права
RUN mkdir -p /tmp/previews && \
    chmod 777 /tmp/previews && \
    mkdir -p /tmp/.config && \
    chmod 777 /tmp/.config && \
    mkdir -p /tmp/.cache && \
    chmod 777 /tmp/.cache && \
    mkdir -p /tmp/.local && \
    chmod 777 /tmp/.local

# Проверка установки LibreOffice
RUN soffice --version
RUN which soffice

# Установка переменных окружения
ENV HOME=/tmp
ENV TMPDIR=/tmp

# Копируем go.mod и go.sum
COPY go.mod go.sum ./

# Скачиваем зависимости
RUN go mod download

# Копируем исходный код
COPY cmd/ cmd/
COPY internal/ internal/
COPY migrations/ migrations/
COPY pkg/ pkg/

# Копируем конфигурационные файлы
COPY .app.env .auth.env .s3.env ./
COPY config/ config/
COPY credentials/ credentials/
COPY migrations/ migrations/

# Сборка приложения с поддержкой CGO
RUN CGO_ENABLED=1 GOOS=linux go build -o main cmd/main.go

EXPOSE 2525
EXPOSE 50051
# Проверяем наличие всех необходимых директорий и прав при запуске
CMD ["sh", "-c", "mkdir -p /tmp/previews && chmod 777 /tmp/previews && ./main"]