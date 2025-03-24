# Trace-For-Otel-Tempo-Grafana-Demo

启动命令：当前目录下执行```docker-compose up -d```

说明：
1. grafana容器启动后访问 http://localhost:3000/ ，并在Data Source配置Tempo数据源为http://tempo:3200(使用service name作为docker网络内寻址)，并执行save && test 按钮进行测试。

