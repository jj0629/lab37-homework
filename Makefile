# Convenience targets. Everything also works with plain `docker compose`.
.PHONY: up down reset logs ps scale test webhook-burst webhook-replay csv demo

up:            ## build and start the stack (UI: http://localhost:8080)
	docker compose up -d --build
	@echo "UI: http://localhost:$${PORT:-8080}"

down:          ## stop containers (keeps data)
	docker compose down

reset:         ## stop and delete all data
	docker compose down -v

logs:
	docker compose logs -f --tail=100 api worker

ps:
	docker compose ps

scale:         ## 3 API replicas, 4 workers
	docker compose up -d --scale api=3 --scale worker=4

test:          ## Go unit tests + frontend type-check
	cd backend && go test ./...
	cd frontend && npm ci --no-audit --no-fund && npm run typecheck

webhook-burst: ## 2,000 new webhook orders, bursty
	docker compose run --rm mock-webhook --fresh --count 2000 --rate 50 --burst 300 --burst-every 3s

webhook-replay: ## replay the sample webhook file verbatim
	docker compose run --rm mock-webhook --rate 100 --burst 200

csv:           ## upload the four sample CSVs in order (re-uploads are no-ops)
	for n in 1 2 3 4; do curl -s -F file=@sample-data/orders_$$n.csv http://localhost:$${PORT:-8080}/api/orders/import; echo; done

demo: webhook-replay csv ## load all sample data
