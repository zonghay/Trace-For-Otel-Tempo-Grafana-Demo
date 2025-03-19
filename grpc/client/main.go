package main

import (
	"context"
	"log"
	"os"
	"time"

	pb "github.com/example/grpc/proto"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	address     = "localhost:50051"
	defaultName = "世界"
)

// 初始化 OpenTelemetry
func initTracer() func() {
	ctx := context.Background()

	// 创建 OTLP exporter
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		log.Fatalf("创建 OTLP exporter 失败: %v", err)
	}

	// 创建资源
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("grpc-client"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		log.Fatalf("创建资源失败: %v", err)
	}

	// 创建 trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Fatalf("关闭 tracer provider 失败: %v", err)
		}
	}
}

func main() {
	// 初始化 OpenTelemetry
	cleanup := initTracer()
	defer cleanup()

	// 设置连接到服务器的选项
	conn, err := grpc.Dial(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		log.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()
	c := pb.NewGreeterClient(conn)

	// 获取命令行参数
	name := defaultName
	if len(os.Args) > 1 {
		name = os.Args[1]
	}

	// 创建上下文
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// 创建一个新的 span
	ctx, span := otel.Tracer("client").Start(ctx, "CallSayHello")
	span.SetAttributes(attribute.String("client.name", name))
	defer span.End()

	// 调用 SayHello
	r, err := c.SayHello(ctx, &pb.HelloRequest{Name: name})
	if err != nil {
		log.Fatalf("调用失败: %v", err)
	}
	log.Printf("响应: %s", r.GetMessage())

	r, err = c.SayHello(ctx, &pb.HelloRequest{Name: name})
	if err != nil {
		log.Fatalf("调用失败: %v", err)
	}
	log.Printf("响应: %s", r.GetMessage())

	// 等待一下确保追踪数据被发送
	time.Sleep(100 * time.Millisecond)
}
