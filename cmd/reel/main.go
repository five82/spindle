// Package main provides the CLI entry point for Reel.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fatih/color"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/discovery"
	"github.com/five82/reel/internal/logging"
	"github.com/five82/reel/internal/processing"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/reporter"
	"github.com/five82/reel/internal/util"
)

const (
	appName    = "reel"
	appVersion = "0.2.0"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "encode":
		if err := runEncode(os.Args[2:]); err != nil {
			if err == context.Canceled {
				fmt.Fprintln(os.Stderr, "Canceled")
				os.Exit(130) // Standard exit code for SIGINT
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Printf("%s version %s\n", appName, appVersion)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func qualityModeDefaultHelp() string {
	if quality.VshipBuildEnabled() {
		return "target"
	}
	return "crf (target unavailable in no_vship build)"
}

func targetQualityOptionsRequested(ea encodeArgs) bool {
	return ea.targetQuality != "" ||
		ea.crfSearchRange != "" ||
		ea.cvvdpDisplay != "" ||
		ea.metricWorkers != 0 ||
		ea.maxProbes != 0 ||
		(ea.qualityModeSet && strings.EqualFold(strings.TrimSpace(ea.qualityMode), config.QualityModeTarget))
}

func printUsage() {
	fmt.Printf(`%s - Video encoding tool

Usage:
  %s <command> [options]

Commands:
  encode    Encode video files to AV1 format
  version   Print version information
  help      Show this help message

Run '%s encode --help' for encode command options.
`, appName, appName, appName)
}

// encodeArgs holds the parsed arguments for the encode command.
type encodeArgs struct {
	inputPath       string
	outputDir       string
	logDir          string
	verbose         bool
	qualityMode     string
	qualityModeSet  bool
	targetQuality   string
	crfSearchRange  string
	cvvdpDisplay    string
	metricWorkers   int
	maxProbes       int
	crf             string // Single value or comma-separated triple (SD,HD,UHD)
	preset          uint
	parallelism     uint
	disableAutocrop bool
	grainTreatment  string
	denoise         string
	probeMetric     string
	fgsTable        string
	noLog           bool
	keepWorkDir     bool
	colorMode       string
}

func runEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Encode video files to AV1 format.

Usage:
  %s encode [options]

Required:
  -i, --input <PATH>     Input video file or directory containing video files
  -o, --output <PATH>    Output directory (or filename if input is a single file)

Options:
  -l, --log-dir <PATH>   Log directory (defaults to ~/.local/state/reel/logs)
  -v, --verbose          Enable verbose output for troubleshooting
  --color <MODE>         Color output: auto, always, or never. Default: auto

Quality Settings:
  --quality-mode <MODE>  Quality mode: target or crf. Default: %s
  --target-quality <R>   CVVDP JOD target range for target mode. Default: %s
  --crf-range <R>        CRF search range for target mode. Default: %s
  --cvvdp-display <PATH> CVVDP display JSON override. Default: generated normal-viewing model
  --metric-workers <N>   Concurrent VSHIP/CUDA scoring workers. Default: %d below 4K, %d for 4K
  --max-probes <N>       Maximum target-quality probes per chunk. Default: %d
  --crf <VALUE>          Fixed CRF quality level (1-70, quarter steps). Accepts:
                           Single value: --crf 25.25 (use for all resolutions)
                           Triple: --crf 24,26.25,26.5 (SD,HD,UHD)
                         Supplying --crf without --quality-mode selects fixed-CRF mode.
                         Fixed defaults: SD=%s, HD=%s, UHD=%s
  --preset <0-13>        SVT-AV1 encoder preset. Lower=slower/better. Default: %d
  --level-of-parallelism <1-6>
                         SVT-AV1 level_of_parallelism override. Bitstream-neutral;
                         affects throughput only. Default: auto-scale from worker target

Processing Options:
  --disable-autocrop     Disable automatic black bar crop detection
  --grain-treatment <M>  auto or off. In target-quality mode, auto measures
                         each title and encodes grainy ones from a denoised
                         source with a film grain table, so the decoder
                         re-synthesizes texture instead of the encoder coding
                         noise. Clean titles are untouched. Default: auto
  --denoise <FILTER>     EXPERIMENTAL: libavfilter graph applied to every frame
                         before encoding, e.g. hqdn3d=2:1.5:3:2.25. Quality
                         metrics score against the denoised source, not the
                         original. Crop and shot-cut detection stay unfiltered.
                         Default: off
  --probe-metric <M>     EXPERIMENTAL: target-quality probe metric: auto or
                         cvvdp. cvvdp disables the SDR <=1080p SSIMULACRA2
                         fast path. Default: auto
  --fgs-table <PATH>     EXPERIMENTAL: libaom "filmgrn1" film grain table to
                         attach to the bitstream (film grain synthesis at
                         decode time). Does not change encoded pixels, rate,
                         or quality scoring. Default: off

Output Options:
  --no-log               Disable Reel log file creation
  --keep-workdir         Keep the .reel work directory after successful encodes
`, appName, qualityModeDefaultHelp(), config.DefaultTargetQuality, config.DefaultCRFSearchRange, config.DefaultMetricWorkersBelowUHD, config.DefaultMetricWorkersUHD, config.DefaultTargetQualityMaxProbes, quality.FormatCRF(config.DefaultCRFSD), quality.FormatCRF(config.DefaultCRFHD), quality.FormatCRF(config.DefaultCRFUHD), config.DefaultSVTAV1Preset)
	}

	var ea encodeArgs

	// Required arguments
	fs.StringVar(&ea.inputPath, "i", "", "Input video file or directory")
	fs.StringVar(&ea.inputPath, "input", "", "Input video file or directory")
	fs.StringVar(&ea.outputDir, "o", "", "Output directory")
	fs.StringVar(&ea.outputDir, "output", "", "Output directory")

	// Optional arguments
	fs.StringVar(&ea.logDir, "l", "", "Log directory")
	fs.StringVar(&ea.logDir, "log-dir", "", "Log directory")
	fs.BoolVar(&ea.verbose, "v", false, "Enable verbose output")
	fs.BoolVar(&ea.verbose, "verbose", false, "Enable verbose output")

	// Quality settings
	fs.Func("quality-mode", "Quality mode: target or crf", func(value string) error {
		ea.qualityMode = value
		ea.qualityModeSet = true
		return nil
	})
	fs.StringVar(&ea.targetQuality, "target-quality", "", "CVVDP JOD target range LOW-HIGH")
	fs.StringVar(&ea.crfSearchRange, "crf-range", "", "Target-quality CRF search range LOW-HIGH")
	fs.StringVar(&ea.cvvdpDisplay, "cvvdp-display", "", "CVVDP display JSON override")
	fs.IntVar(&ea.metricWorkers, "metric-workers", 0, "Concurrent VSHIP/CUDA scoring workers")
	fs.IntVar(&ea.maxProbes, "max-probes", 0, "Maximum target-quality probes per chunk")
	fs.StringVar(&ea.crf, "crf", "", "Fixed CRF quality level (single value or SD,HD,UHD)")
	fs.UintVar(&ea.preset, "preset", 0, "SVT-AV1 encoder preset (0-13)")
	fs.UintVar(&ea.parallelism, "level-of-parallelism", 0, "SVT-AV1 level_of_parallelism (1-6); 0 auto-scales from the worker target")

	// Processing options
	fs.BoolVar(&ea.disableAutocrop, "disable-autocrop", false, "Disable automatic crop detection")
	fs.StringVar(&ea.grainTreatment, "grain-treatment", config.GrainTreatmentAuto, "Automatic denoise plus film grain synthesis for grainy titles: auto or off")
	fs.StringVar(&ea.denoise, "denoise", "", "EXPERIMENTAL: libavfilter graph applied before encoding and to metric reference frames")
	fs.StringVar(&ea.probeMetric, "probe-metric", "", "EXPERIMENTAL: target-quality probe metric: auto or cvvdp")
	fs.StringVar(&ea.fgsTable, "fgs-table", "", "EXPERIMENTAL: libaom filmgrn1 film grain table path")
	// Output options
	fs.BoolVar(&ea.noLog, "no-log", false, "Disable log file creation")
	fs.BoolVar(&ea.keepWorkDir, "keep-workdir", false, "Keep the .reel work directory after successful encodes")
	fs.StringVar(&ea.colorMode, "color", "auto", "Color output mode: auto, always, never")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate required arguments
	if ea.inputPath == "" {
		return fmt.Errorf("input path is required (-i/--input)")
	}
	if ea.outputDir == "" {
		return fmt.Errorf("output directory is required (-o/--output)")
	}

	return executeEncode(ea)
}

func executeEncode(ea encodeArgs) error {
	return executeEncodeWithProcess(ea, processing.ProcessVideos)
}

func executeEncodeWithProcess(ea encodeArgs, process func(context.Context, *config.Config, []string, string, reporter.Reporter) ([]processing.EncodeResult, error)) error {
	// Resolve input path
	inputPath, err := filepath.Abs(ea.inputPath)
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	// Check if input exists
	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		return fmt.Errorf("input path does not exist: %s", inputPath)
	}

	// Resolve output path
	outputDir, targetFilename, err := resolveOutputPath(inputPath, ea.outputDir, inputInfo.IsDir())
	if err != nil {
		return err
	}

	// Ensure output directory exists
	if err := util.EnsureDirectory(outputDir); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Resolve log directory
	logDir := ea.logDir
	if logDir == "" {
		logDir = logging.DefaultLogDir()
	}

	// Setup file logging
	logger, err := logging.Setup(logDir, ea.verbose, ea.noLog, os.Args)
	if err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}
	if logger != nil {
		defer func() { _ = logger.Close() }()
	}

	// Discover files to process
	var filesToProcess []string
	if inputInfo.IsDir() {
		filesToProcess, err = discovery.FindVideoFiles(inputPath)
		if err != nil {
			return fmt.Errorf("failed to discover video files: %w", err)
		}
		if len(filesToProcess) == 0 {
			return fmt.Errorf("no video files found in %s", inputPath)
		}
		if logger != nil {
			logger.Info("Discovered %d video files in %s", len(filesToProcess), inputPath)
			for i, f := range filesToProcess {
				logger.Debug("  %d. %s", i+1, f)
			}
		}
	} else {
		filesToProcess = []string{inputPath}
		if logger != nil {
			logger.Info("Processing single file: %s", inputPath)
		}
	}

	// Build configuration
	cfg := config.NewConfig(inputPath, outputDir, logDir)
	if logger != nil {
		cfg.LogFile = logger.FilePath()
	}

	if !quality.VshipBuildEnabled() && targetQualityOptionsRequested(ea) {
		return fmt.Errorf("target-quality options are not available in no_vship builds; rebuild with libvship support or use --quality-mode crf")
	}

	// Override with explicit CLI arguments
	if ea.qualityModeSet {
		cfg.QualityMode = strings.ToLower(strings.TrimSpace(ea.qualityMode))
	}
	if ea.targetQuality != "" {
		cfg.TargetQuality = ea.targetQuality
	}
	if ea.crfSearchRange != "" {
		cfg.CRFSearchRange = ea.crfSearchRange
	}
	if ea.cvvdpDisplay != "" {
		cfg.CVVDPDisplay = ea.cvvdpDisplay
	}
	if ea.metricWorkers != 0 {
		cfg.MetricWorkers = ea.metricWorkers
	}
	if ea.maxProbes != 0 {
		cfg.TargetQualityMaxProbes = ea.maxProbes
	}
	if ea.crf != "" {
		if !ea.qualityModeSet {
			cfg.QualityMode = config.QualityModeCRF
		}
		if err := parseCRF(ea.crf, cfg); err != nil {
			return err
		}
	}
	if ea.preset != 0 {
		cfg.SVTAV1Preset = uint8(ea.preset)
	}
	if ea.parallelism != 0 {
		cfg.SVTAV1LevelOfParallelism = uint32(ea.parallelism)
	}
	if ea.disableAutocrop {
		cfg.CropMode = "none"
	}
	cfg.GrainTreatment = strings.ToLower(strings.TrimSpace(ea.grainTreatment))
	cfg.Denoise = strings.TrimSpace(ea.denoise)
	switch pm := strings.ToLower(strings.TrimSpace(ea.probeMetric)); pm {
	case "", "auto":
		cfg.ProbeMetric = ""
	case "cvvdp":
		cfg.ProbeMetric = pm
	default:
		return fmt.Errorf("invalid --probe-metric %q: must be auto or cvvdp", ea.probeMetric)
	}
	cfg.GrainTable = strings.TrimSpace(ea.fgsTable)
	// Debug options
	cfg.Verbose = ea.verbose
	cfg.KeepWorkDir = ea.keepWorkDir

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Log configuration
	if logger != nil {
		logger.Info("Output directory: %s", outputDir)
		logger.Info("Quality mode: %s", cfg.QualityMode)
		if cfg.QualityMode == config.QualityModeTarget {
			logger.Info("Target quality: %s (target %.2f +/- %.2f), CRF range %s", cfg.TargetQuality, cfg.TargetQualityTarget, cfg.TargetQualityTolerance, cfg.CRFSearchRange)
		} else {
			logger.Info("CRF quality: SD=%s, HD=%s, UHD=%s", quality.FormatCRF(cfg.CRFSD), quality.FormatCRF(cfg.CRFHD), quality.FormatCRF(cfg.CRFUHD))
		}
		logger.Info("SVT-AV1 preset: %d", cfg.SVTAV1Preset)
		logger.Info("Crop mode: %s", cfg.CropMode)
		logger.Info("Grain treatment: %s", cfg.GrainTreatment)
		if cfg.Denoise != "" {
			logger.Info("Denoise filter (experimental): %s", cfg.Denoise)
		}
		logger.Info("Adaptive encoding enabled")
	}

	// Configure color output
	switch ea.colorMode {
	case "always":
		color.NoColor = false
	case "never":
		color.NoColor = true
	}

	// Create reporters
	termRep := reporter.NewTerminalReporterVerbose(ea.verbose)
	var rep reporter.Reporter = termRep
	if logger != nil {
		// Combine terminal and log reporter so all events go to both
		logRep := reporter.NewLogReporter(logger.Writer())
		rep = reporter.NewCompositeReporter(termRep, logRep)
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Run encoding
	_, err = process(ctx, cfg, filesToProcess, targetFilename, rep)
	return err
}

// resolveOutputPath determines the output directory and optional target filename.
// If input is a file and output has a video extension, treat output as target filename.
func resolveOutputPath(_, outputPath string, isInputDir bool) (outputDir, targetFilename string, err error) {
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return "", "", fmt.Errorf("invalid output path: %w", err)
	}

	// If input is a directory, output must be a directory
	if isInputDir {
		return outputPath, "", nil
	}

	// Check if output path looks like a file (has video extension)
	ext := filepath.Ext(outputPath)
	videoExtensions := map[string]bool{
		".mkv": true, ".mp4": true, ".webm": true,
		".avi": true, ".mov": true, ".m4v": true,
	}

	if videoExtensions[ext] {
		// Output is a target filename
		return filepath.Dir(outputPath), filepath.Base(outputPath), nil
	}

	// Output is a directory
	return outputPath, "", nil
}

// parseCRF parses the CRF string and applies it to the config.
// Accepts either a single value (applied to all resolutions) or a comma-separated triple (SD,HD,UHD).
func parseCRF(crfStr string, cfg *config.Config) error {
	parts := strings.Split(crfStr, ",")

	switch len(parts) {
	case 1:
		// Single value: apply to all resolutions
		val, err := quality.ParseCRF(parts[0])
		if err != nil {
			return fmt.Errorf("invalid CRF value %q: %w", crfStr, err)
		}
		cfg.CRFSD = val
		cfg.CRFHD = val
		cfg.CRFUHD = val
	case 3:
		// Triple: SD,HD,UHD
		vals := make([]float32, 3)
		for i, part := range parts {
			val, err := quality.ParseCRF(strings.TrimSpace(part))
			if err != nil {
				return fmt.Errorf("invalid CRF value in position %d: %w", i+1, err)
			}
			vals[i] = val
		}
		cfg.CRFSD = vals[0]
		cfg.CRFHD = vals[1]
		cfg.CRFUHD = vals[2]
	default:
		return fmt.Errorf("--crf accepts single value or comma-separated triple (SD,HD,UHD), got %d values", len(parts))
	}

	return nil
}
