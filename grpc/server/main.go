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
	"go.opentelemetry.io/otel/trace"
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
	// 获取当前的 span (Span A - 服务端接收到的根 span)
	ctx, spanA := otel.Tracer("server").Start(ctx, "SayHello")
	defer spanA.End()
	// 添加一些属性到 span
	spanA.SetAttributes(attribute.String("request.name", in.GetName()))
	spanA.AddEvent("Processing request", trace.WithAttributes(attribute.String("event.type", "process_start")))

	// 创建 Span B (ChildOf Span A)
	_, spanB := otel.Tracer("server").Start(ctx, "ProcessMetadata")
	spanB.SetAttributes(attribute.String("span.type", "parallel_process"))
	// 模拟一些处理时间
	time.Sleep(20 * time.Millisecond)
	spanB.End()

	// 创建 Span C (ChildOf Span A)
	ctxC, spanC := otel.Tracer("server").Start(ctx, "BusinessLogic")
	spanC.SetAttributes(attribute.String("span.type", "main_process"))

	// 模拟 DB 请求行为 (Span D - ChildOf Span C)
	_, spanD := otel.Tracer("server").Start(ctxC, "DatabaseQuery")
	spanD.SetAttributes(
		attribute.String("db.system", "mysql"),
		attribute.String("db.operation", "select"),
		attribute.String("db.statement", "SELECT * FROM users WHERE name = ?"),
	)

	// 模拟数据库查询时间
	time.Sleep(30 * time.Millisecond)
	spanD.AddEvent("Database query completed", trace.WithAttributes(attribute.Int("db.rows_affected", 1)))
	spanD.End()

	// 模拟数据处理 (Span E - ChildOf Span C)
	_, spanE := otel.Tracer("server").Start(ctxC, "ProcessData")
	spanE.SetAttributes(attribute.String("process.type", "data_transformation"))

	// 模拟处理时间
	time.Sleep(25 * time.Millisecond)
	spanE.AddEvent("Data processing completed")

	// 在 Span E 结束前，创建一个 FollowFrom 类型的 Span F (使用同一个 Trace ID)
	spanEContext := spanE.SpanContext()
	// 结束 Span E
	spanE.End()
	// 结束 Span C
	spanC.End()

	// 创建 Span F，保持在同一个 Trace 中
	ctxF, spanF := otel.Tracer("server").Start(
		ctx, // 使用原始上下文保持在同一个 Trace 中
		"AsyncPostProcess",
		trace.WithLinks(trace.Link{
			SpanContext: spanEContext,
			Attributes:  []attribute.KeyValue{attribute.String("link.relation", "follows_from")},
		}),
	)

	// 模拟异步后处理操作
	go func(ctx context.Context, span trace.Span) {
		defer span.End()

		// 模拟异步处理时间
		time.Sleep(40 * time.Millisecond)
		span.AddEvent("Async processing completed")

		log.Printf("Async processing for request '%s' completed", in.GetName())
	}(ctxF, spanF)

	// 记录请求处理完成
	log.Printf("Received: %v", in.GetName())
	spanA.AddEvent("Request processed successfully")

	return &pb.HelloReply{Message: "Hello " + in.GetName()}, nil
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
		log.Fatalf("Failed to create OTLP exporter: %v", err)
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
		log.Fatalf("Failed to create resource: %v", err)
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
			log.Fatalf("Failed to shutdown tracer provider: %v", err)
		}
	}
}

func main() {
	// 初始化 OpenTelemetry
	cleanup := initTracer()
	defer cleanup()

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// 创建 gRPC 服务器并添加 OpenTelemetry 拦截器
	s := grpc.NewServer(
		grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
		grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
	)

	pb.RegisterGreeterServer(s, &server{})
	log.Printf("Server started on %v", port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
