包: P-V4-http-mongo-integration
状态: done
尝试: 单元 1(新建文件) 1 次; 单元 2 跑 1 次即过, 合计 2 次
验收: 全部通过
关键数字:
  - 子测试 1-jwt: PASS (401/401/401/200)
  - 子测试 2-at-binding-forgery: PASS (_id=u1, 伪造剥离=yes)
  - 子测试 3-permission: PASS (deny=403, auto-auth=200)
  - 子测试 4-data-commands: PASS (HSET→HGET→HDEL→404)
  - 子测试 5-api-at-mongo: PASS (saved=hello, owner=u1, 伪造body @uid剥离=yes, Mongo 落盘 text=hello)
  - 子测试 6-codec-mapping: PASS (HTTP 字段集 = BSON 字段集 = [_id createdAt name role updatedAt]; _id=string "u1"; role=member 两层一致; createdAt=BSON Date 非 string)
  - 输出含 "INTEGRATION OK", 无 SKIP, 0.25s
产物: 新建 httpserve/http_integration_test.go; http_mongo.txt 留痕
招数: 单元 1 逐字创建; 单元 2 DOPTIME_TEST_MONGO_URI=mongodb://localhost:27017 go test -run TestHTTPMongo -v ./httpserve
经验: V4 在真 Mongo 上六项契约一次全过, codec 字段映射(BSON tag vs JSON tag)完全对齐
异常发现: 无
