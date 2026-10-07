# Generic example.com fixtures only.
curl -X POST 'https://example.com/api/users?active=1' \
  -H 'Content-Type: application/json' \
  -H "Accept: application/json" \
  -H 'X-Request-Id: {{reqId}}' \
  --data-raw '{"name":"alice","tags":["a","b"]}' --compressed

curl -sSL -k --max-time 2.5 https://example.com/redirect

curl -u alice:s3cret-canary https://example.com/basic
curl -G https://example.com/search --data-urlencode 'q=hello world' -d 'page=2'
curl https://example.com/upload -F 'title=report' -F 'file=@/home/me/report.pdf;type=application/pdf'
curl -XPUT https://example.com/items/1 -d @body.json
curl -I https://example.com/health
curl https://example.com/h -H 'Authorization: Bearer CANARY-TOKEN' -b 'a=1; b=2' -A 'probe/1.0' --proxy http://127.0.0.1:3128 --unknown-thing
curl "https://example.com/$API_PATH" | jq .
echo hello
curl --json '{"k":1}' https://example.com/json
