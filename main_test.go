package main

// 文件服务相关的测试（blockDotfiles、startFileServer 及其上下游）都挪到了
// fileserver_test.go，跟着实现代码一起搬到了 fileserver.go。main.go 现在只剩
// 启动流程编排，本身没有单独值得测的纯函数，交由 tunnel_test.go 之类的
// 集成性测试间接覆盖。
