.PHONY: help db-up db-down db-reset db-psql db-info migrate-up migrate-down migrate-status dump restore csv-download test test-db test-unit

# ⚠️ migrate-* / dump / restore / csv-download 는 커밋되지 않는 디렉토리를 다룬다
#    (/migrations, /pipeline, backups/, data/raw/ — .gitignore 참고).
#    새로 클론한 곳에서는 동작하지 않는다. DB 는 덤프 복구로 세운다:
#      make db-up && make restore FILE=backups/tripaw-YYYYmmdd-HHMMSS.dump

# .env 의 DATABASE_URL 등을 로드
include .env
export

GOOSE := go run github.com/pressly/goose/v3/cmd/goose@latest
STAMP := $(shell date +%Y%m%d-%H%M%S)

help:
	@grep -hE '^[a-z-]+:.*?##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | column -t -s "$$(printf '\t')"

# ── DB 컨테이너 ────────────────────────────────────────────────
db-up:        ## PostGIS 컨테이너 기동 (healthy 될 때까지 대기)
	docker compose up -d
	@printf "기동 대기"
	@until [ "$$(docker inspect -f '{{.State.Health.Status}}' tripaw-db 2>/dev/null)" = "healthy" ]; do \
		printf "."; sleep 1; \
	done; echo " ready"

db-down:      ## 컨테이너 중지 (데이터 유지)
	docker compose down

db-psql:      ## psql 접속
	docker compose exec db psql -U tripaw -d tripaw

db-info:      ## 확장/테이블/적재 건수 확인
	@docker compose exec -T db psql -U tripaw -d tripaw -c \
		"SELECT extname, extversion FROM pg_extension ORDER BY extname;"
	@docker compose exec -T db psql -U tripaw -d tripaw -c \
		"SELECT relname AS table, n_live_tup AS rows FROM pg_stat_user_tables ORDER BY relname;"

# ⚠️ 데이터를 삭제한다. 수집 데이터는 재수집에 3일치 쿼터가 필요하므로
#    반드시 dump 를 먼저 남길 것.
db-reset:     ## [위험] 볼륨까지 삭제하고 재생성
	@echo "볼륨을 삭제합니다. 수집 데이터가 사라집니다."
	@echo "계속하려면 10초 내에 Ctrl-C 를 누르지 마세요..."
	@sleep 10
	docker compose down -v
	$(MAKE) db-up
	$(MAKE) migrate-up

# ── 마이그레이션 ───────────────────────────────────────────────
migrate-up:     ## 마이그레이션 적용
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up

migrate-down:   ## 마이그레이션 1단계 롤백
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" down

migrate-status: ## 마이그레이션 상태
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" status

# ── 테스트 ────────────────────────────────────────────────────
# 통합 테스트는 진짜 Postgres 를 쓴다. 저장소 계층의 SQL(UPSERT, DELETE ... RETURNING)은
# 가짜 DB 로는 검증이 안 되기 때문이다. tripaw 본 DB 와 분리된 tripaw_test 를 쓴다.
TEST_DATABASE_URL := postgres://tripaw:tripaw_local@localhost:5432/tripaw_test?sslmode=disable

test-db:      ## 테스트용 DB 생성 + 마이그레이션 적용
	@docker compose exec -T db psql -U tripaw -d postgres -tAc \
		"SELECT 1 FROM pg_database WHERE datname='tripaw_test'" | grep -q 1 || \
		docker compose exec -T db psql -U tripaw -d postgres -c "CREATE DATABASE tripaw_test;"
	$(GOOSE) -dir migrations postgres "$(TEST_DATABASE_URL)" up

# -p 1 은 패키지를 한 번에 하나씩 돌린다. 통합 테스트가 tripaw_test 하나를
# 공유하면서 각자 TRUNCATE 로 초기화하는데, 패키지가 병렬로 돌면 서로의 데이터를
# 지워서 FK 위반과 데드락이 난다. 패키지별 DB 를 만드는 방법도 있지만
# 전체 12초짜리 스위트에 그만한 장치를 붙일 이유가 없다.
test:         ## 전체 테스트 (TEST_DATABASE_URL 이 없으면 통합 테스트는 건너뜀)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -p 1 ./...

test-unit:    ## DB 없이 도는 테스트만
	go test ./...

# ── 백업 / 복구 ────────────────────────────────────────────────
# 수집은 관광공사 일 1,000건 한도 때문에 3일 이상 걸린다.
# 볼륨이 날아가면 그 3일을 다시 써야 하므로, 각 소스 수집이 끝날 때마다 dump 를 남긴다.
dump:         ## backups/ 에 덤프 생성
	@mkdir -p backups
	docker compose exec -T db pg_dump -U tripaw -d tripaw -Fc > backups/tripaw-$(STAMP).dump
	@ls -lh backups/tripaw-$(STAMP).dump

restore:      ## 덤프 복구 (FILE=backups/xxx.dump)
	@test -n "$(FILE)" || (echo "사용법: make restore FILE=backups/tripaw-YYYYmmdd-HHMMSS.dump"; exit 1)
	docker compose exec -T db pg_restore -U tripaw -d tripaw --clean --if-exists < $(FILE)

# ── 공공데이터 원본 ─────────────────────────────────────────
# 문화정보원 CSV. atchFileId 는 파일이 갱신되면 바뀌므로 상세페이지에서 매번 파싱한다.
KCISA_PAGE := https://www.data.go.kr/data/15111389/fileData.do
KCISA_CSV  := data/raw/kcisa_pet_facilities.csv

csv-download: ## 문화정보원 반려동물 동반 가능 시설 CSV 내려받기 (30MB)
	@mkdir -p data/raw
	@echo "상세페이지에서 atchFileId 확인 중..."
	@fid=$$(curl -sL -A 'Mozilla/5.0' '$(KCISA_PAGE)' \
	  | grep -o 'atchFileId=FILE_[0-9]*' | head -1 | cut -d= -f2); \
	if [ -z "$$fid" ]; then echo "atchFileId 를 찾지 못했습니다. 페이지 구조가 바뀐 듯합니다."; exit 1; fi; \
	echo "atchFileId=$$fid"; \
	curl -sL -A 'Mozilla/5.0' -e '$(KCISA_PAGE)' \
	  "https://www.data.go.kr/cmm/cmm/fileDownload.do?atchFileId=$$fid&fileDetailSn=1&insertDataPrcus=N" \
	  -o '$(KCISA_CSV)'
	@wc -l '$(KCISA_CSV)'
