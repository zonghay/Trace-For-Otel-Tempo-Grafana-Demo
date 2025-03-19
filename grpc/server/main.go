package main

import (
	"context"
	"log"
	"net"
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
)

const (
	port = ":50051"
)

// 服务实现
type server struct {
	pb.UnimplementedGreeterServer
}

// SayHello 实现
func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	// 获取当前的 span
	ctx, span := otel.Tracer("server").Start(ctx, "SayHello")
	defer span.End()

	// 添加一些属性到 span
	span.SetAttributes(attribute.String("request.name", in.GetName()))

	// 模拟处理时间
	time.Sleep(50 * time.Millisecond)

	log.Printf("收到: %v", in.GetName())
	return &pb.HelloReply{Message: "你好 " + in.GetName()}, nil
}

// 初始化 OpenTelemetry
func initTracer() func() {
	ctx := context.Background()

	// 创建 OTLP exporter，增加超时和重试选项
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("localhost:4317"),
		otlptracegrpc.WithTimeout(10*time.Second), // 增加超时时间
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     5 * time.Second,
			MaxElapsedTime:  30 * time.Second,
		}),
	)
	if err != nil {
		log.Fatalf("创建 OTLP exporter 失败: %v", err)
	}

	// 创建资源
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("grpc-server"),
			semconv.ServiceVersion("1.0.0"),
			semconv.TelemetrySDKLanguageGo,
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

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}

	// 创建 gRPC 服务器并添加 OpenTelemetry 拦截器
	s := grpc.NewServer(
		grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
		grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
	)

	pb.RegisterGreeterServer(s, &server{})
	log.Printf("服务器启动在 %v", port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("服务失败: %v", err)
	}
}
