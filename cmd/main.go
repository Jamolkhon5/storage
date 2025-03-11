package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"synxrondrive/internal/preview"
	pb "synxrondrive/pkg/proto/recording_v1"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"synxrondrive/internal/auth"
	"synxrondrive/internal/config"
	"synxrondrive/internal/handler"
	"synxrondrive/internal/repository"
	"synxrondrive/internal/service"
	"synxrondrive/internal/service/s3"
)

func connectWithRetry(dsn string, maxAttempts int, delay time.Duration) (*sqlx.DB, error) {
	// Сначала подключаемся к базе postgres (системная база, которая всегда существует)
	pgDSN := strings.Replace(dsn, "dbname=filemanager", "dbname=postgres", 1)
	pgDB, err := sqlx.Connect("postgres", pgDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres database: %v", err)
	}
	defer pgDB.Close()

	// Проверяем, существует ли база данных filemanager
	var exists bool
	err = pgDB.Get(&exists, "SELECT EXISTS(SELECT datname FROM pg_catalog.pg_database WHERE datname = 'filemanager')")
	if err != nil {
		return nil, fmt.Errorf("failed to check database existence: %v", err)
	}

	// Если базы нет, создаем её
	if !exists {
		log.Println("Database filemanager does not exist, creating...")
		_, err = pgDB.Exec("CREATE DATABASE filemanager")
		if err != nil {
			return nil, fmt.Errorf("failed to create database: %v", err)
		}
	}

	// Теперь пытаемся подключиться к базе filemanager
	var db *sqlx.DB
	for i := 0; i < maxAttempts; i++ {
		db, err = sqlx.Connect("postgres", dsn)
		if err == nil {
			return db, nil
		}

		log.Printf("Failed to connect to database (attempt %d/%d): %v", i+1, maxAttempts, err)
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("failed to connect after %d attempts: %v", maxAttempts, err)
}

func runMigrations(cfg *config.Config) error {
	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.Name,
		cfg.Database.SSLMode,
	)

	var m *migrate.Migrate
	var err error

	for i := 0; i < 5; i++ {
		m, err = migrate.New("file://migrations", databaseURL)
		if err == nil {
			break
		}
		log.Printf("Failed to create migrate instance (attempt %d/5): %v", i+1, err)
		time.Sleep(time.Second * 5)
	}

	if err != nil {
		return fmt.Errorf("failed to create migrate instance after retries: %w", err)
	}
	defer m.Close()

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		log.Printf("Found dirty database state at version %d, attempting to force version", version)
		if err := m.Force(int(version)); err != nil {
			return fmt.Errorf("failed to force version: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func main() {
	// Загружаем конфигурации
	appConfig, err := config.NewConfig(".app.env")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Подключаемся к базе данных
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		appConfig.Database.Host,
		appConfig.Database.Port,
		appConfig.Database.User,
		appConfig.Database.Password,
		appConfig.Database.Name,
		appConfig.Database.SSLMode,
	)

	db, err := connectWithRetry(dsn, 5, time.Second*5)
	if err != nil {
		log.Fatalf("Failed to connect to database after retries: %v", err)
	}
	defer db.Close()

	if err := runMigrations(appConfig); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// Инициализация S3 клиента
	s3Config, err := s3.NewConfig(".s3.env")
	if err != nil {
		log.Fatalf("Failed to load S3 config: %v", err)
	}

	s3Client, err := s3.NewClient(s3Config)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	// Подключение к сервису аутентификации
	authConfig, err := auth.NewConfig(".auth.env")
	if err != nil {
		log.Fatalf("Failed to load auth config: %v", err)
	}

	conn, err := grpc.Dial(authConfig.AuthAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to auth service: %v", err)
	}
	defer conn.Close()

	auth.InitClient(conn)

	// Инициализация репозиториев
	fileRepo := repository.NewFileRepository(db)
	folderRepo := repository.NewFolderRepository(db)
	shareRepo := repository.NewShareRepository(db)
	trashRepo := repository.NewTrashRepository(db)
	quotaRepo := repository.NewStorageQuotaRepository(db)

	// Инициализация сервисов
	permissionService := service.NewPermissionService(shareRepo, fileRepo, folderRepo)
	folderService := service.NewFolderService(folderRepo, fileRepo, permissionService)
	shareService := service.NewShareService(shareRepo, fileRepo, folderRepo)
	quotaService := service.NewStorageQuotaService(quotaRepo)
	trashService := service.NewTrashService(trashRepo, fileRepo, folderRepo, s3Client, quotaService)
	previewService := preview.NewService(s3Client, db)
	previewService.StartCleanupTask()
	fileService := service.NewFileService(fileRepo, folderRepo, shareRepo, s3Client, permissionService, quotaService)
	videoService, err := service.NewVideoService(fileService, appConfig.Server.VideoDir)
	if err != nil {
		log.Fatalf("Failed to create video service: %v", err)
	}

	// Инициализация хендлеров
	fileHandler := handler.NewFileHandler(fileService, folderService, trashService, videoService)
	folderHandler := handler.NewFolderHandler(folderService, trashService)
	shareHandler := handler.NewShareHandler(shareService)
	trashHandler := handler.NewTrashHandler(trashService)
	previewHandler := preview.NewHandler(previewService, fileService)
	quotaHandler := handler.NewStorageQuotaHandler(quotaService)

	// Настройка HTTP роутера
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Minute))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("Incoming request: %s %s", r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	})

	// HTTP маршруты
	r.Route("/v1", func(r chi.Router) {
		r.Post("/files/context", fileHandler.UploadContextFile)
		r.Post("/files", fileHandler.UploadFile)
		r.Get("/files/exists", fileHandler.CheckFileExists)

		r.Route("/files/{uuid}", func(r chi.Router) {
			r.Put("/rename", fileHandler.RenameFile)
			r.Put("/move", fileHandler.MoveFile)
			r.Get("/", fileHandler.DownloadFile)
			r.Delete("/", fileHandler.DeleteFile)
			r.Get("/preview", previewHandler.GetPreview)
			r.Get("/versions", fileHandler.GetFileVersions)
		})

		r.Route("/videos", func(r chi.Router) {
			r.Get("/stream/{uuid}", fileHandler.StreamVideo)
		})

		r.Get("/folders", folderHandler.GetFolderContent)
		r.Get("/folders/structure", folderHandler.GetFolderStructure)
		r.Post("/folders", folderHandler.CreateFolder)
		r.Get("/folders/{id}", folderHandler.GetFolderContent)
		r.Delete("/folders/{id}", folderHandler.DeleteFolder)
		r.Get("/files/progress", fileHandler.GetUploadProgress)
		r.Put("/folders/{id}/rename", folderHandler.RenameFolder)
		r.Put("/folders/{id}/move", folderHandler.MoveFolder)

		r.Route("/trash", func(r chi.Router) {
			r.Get("/", trashHandler.GetTrashItems)
			r.Post("/empty", trashHandler.EmptyTrash)
			r.Post("/restore", trashHandler.RestoreItem)
			r.Post("/delete", trashHandler.DeletePermanently)
			r.Get("/settings", trashHandler.GetSettings)
			r.Put("/settings", trashHandler.UpdateSettings)
		})

		r.Route("/quota", func(r chi.Router) {
			r.Get("/", quotaHandler.GetQuotaInfo)
			r.Put("/limit", quotaHandler.UpdateQuotaLimit)
		})

		r.Route("/shares", func(r chi.Router) {
			r.Post("/", shareHandler.CreateShare)
			r.Get("/shared-with-me", shareHandler.GetSharedWithMe)
			r.Get("/{id}/structure", shareHandler.GetSharedFolderStructure)
			r.Get("/{id}", shareHandler.GetSharedResource)

			r.Route("/token/{token}", func(r chi.Router) {
				r.Get("/", shareHandler.GetSharedResource)
				r.Post("/access", shareHandler.GrantAccess)
				r.Get("/access", shareHandler.GetSharedFolderContent)
			})
		})
	})

	// Создаем и настраиваем gRPC сервер
	grpcServer := grpc.NewServer()

	// Регистрируем gRPC сервисы
	recordingRepo := repository.NewRecordingRepository(db)
	recordingService := service.NewRecordingService(
		recordingRepo,
		fileRepo,    // Используем fileRepo вместо fileService
		folderRepo,  // Используем folderRepo вместо s3Client
		fileService, // Добавляем fileService
		s3Client,    // Добавляем s3Client
	)
	recordingHandler := handler.NewRecordingHandler(recordingService)
	pb.RegisterRecordingServiceServer(grpcServer, recordingHandler)

	// Создаем HTTP сервер
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", appConfig.Server.Port),
		Handler: r,
	}

	// Канал для сигналов завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Запускаем gRPC сервер
	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%s", appConfig.Server.GRPCPort))
		if err != nil {
			log.Fatalf("Failed to listen for gRPC: %v", err)
		}
		log.Printf("Starting gRPC server on port %s", appConfig.Server.GRPCPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve gRPC: %v", err)
		}
	}()

	// Запускаем HTTP сервер
	go func() {
		log.Printf("Starting HTTP server on port %s", appConfig.Server.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	// Запускаем очистку корзины
	cleanupTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			select {
			case <-cleanupTicker.C:
				ctx := context.Background()
				if err := trashService.AutoCleanup(ctx); err != nil {
					log.Printf("Error during trash auto cleanup: %v", err)
				}
			case <-quit:
				cleanupTicker.Stop()
				return
			}
		}
	}()

	// Ожидаем сигнал завершения
	<-quit
	log.Println("Shutting down servers...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Останавливаем HTTP сервер
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server forced to shutdown: %v", err)
	}

	// Останавливаем gRPC сервер
	grpcServer.GracefulStop()

	// Закрываем соединение с БД
	if err := db.Close(); err != nil {
		log.Printf("Error closing database connection: %v", err)
	}

	log.Println("Server exited properly")
}
