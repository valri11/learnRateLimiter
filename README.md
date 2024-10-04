
### Learn different rate limiting implementations

Educational project to evaluate distributed rate limiters.
Implemented:
 - Fixed Window Limit
 - Sliding Window Log
 - Token Bucket
 - Adaptive Token Bucket

Unit tests:
```sh
go test ./...
```

Build app:
```sh
go mod tidy
go build
```

Start app:
```sh
./learnRateLimiter --config=app-dev.yml server
```

Start infra (redis, grafana, prometheus)
```sh
docker compose -f docker-compose-infra.yml up
```

Run load client:
```sh
cd k6
source ./init_env
k6 run ./smoke-test.js
```

Open browser at <http://localhost:3000/> and check request metrics in dashboard.


## Resources

<https://www.ietf.org/archive/id/draft-polli-ratelimit-headers-02.html>
<https://redis.io/ebook/redis-in-action>
<https://techblog.criteo.com/distributed-rate-limiting-algorithms-a35f7e24783>
<https://blog.callr.tech/rate-limiting-for-distributed-systems-with-redis-and-lua>
<https://systemsdesign.cloud/SystemDesign/RateLimiter>
<https://bytebytego.com/courses/system-design-interview/design-a-rate-limiter>
<https://blog.bytebytego.com/p/rate-limiter-for-the-real-world>
<https://github.com/go-redis/redis_rate>
<https://stripe.com/blog/rate-limiters>
<https://gist.github.com/ptarjan/e38f45f2dfe601419ca3af937fff574d>
<https://engineering.classdojo.com/blog/2015/02/06/rolling-rate-limiter>
<https://www.figma.com/blog/an-alternative-approach-to-rate-limiting/>
<https://blog.cloudflare.com/counting-things-a-lot-of-different-things>
<https://trycatch22.net/counter-sliding-window/>
<https://blogs.halodoc.io/taming-the-traffic-redis-and-lua-powered-sliding-window-rate-limiter-in-action/>
<https://redis.io/learn/develop/dotnet/aspnetcore/rate-limiting/sliding-window>
<https://github.com/ietf-wg-httpapi/ratelimit-headers>



