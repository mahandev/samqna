.PHONY: dev test int build docker deploy backup

dev:
	air

test:
	go test ./... -count=1

int:
	WHISPER_BIN=$$WHISPER_BIN WHISPER_MODEL_PATH=$$WHISPER_MODEL_PATH \
	  go test -tags=integration ./... -v -count=1

build:
	CGO_ENABLED=1 go build -o samqna .

docker:
	docker compose build

deploy:
	docker compose up -d --build

backup:
	mkdir -p ./data/backups
	sqlite3 ./data/samqna.db ".backup ./data/backups/samqna-$$(date +%F).db"
