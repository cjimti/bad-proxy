# Packaging

```shell
export BP_VERSION=1.4.0
docker build --build-arg version=${BP_VERSION} -t txn2/bad-proxy:${BP_VERSION} .
docker push txn2/bad-proxy:${BP_VERSION}
```