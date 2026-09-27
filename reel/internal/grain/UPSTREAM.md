# Noise-model provenance

`noise_model.c`, `noise_model.h`, `grain_params.h`, and `mathutils.h` are
extracted from libaom commit
[`023743a7d78d9c7d33390c5c45b35495ea013a21`](https://aomedia.googlesource.com/aom/+/023743a7d78d9c7d33390c5c45b35495ea013a21/).
Original paths are under `aom_dsp/`. The upstream BSD-2-Clause license and
AOM patent license are preserved in `LICENSE` and `PATENTS` in this directory.

The extraction retains the planar block fit, AR/strength solvers, and grain
parameter quantization. Reel supplies its own bounded native-patch sampler in
`estimator.c`; the original/denoised pairs come from Reel's actual fftdnoiz pass.
No libaom encoder, denoiser, shared-library ABI, or Rust toolchain is required.

Local changes to the extracted code:

- Prefix external symbols with `reel_aom_`; replace private libaom allocation,
  clamp, and enum helpers with standard C equivalents.
- Remove the denoiser, whole-frame flat-block scan, temporal model switching,
  unused utilities, and stderr diagnostics. Fit one pooled patch mosaic.
- Restore the strength solver's RHS after temporary regularization, reject
  zero-observation solves and non-finite solutions, and clean up a failed
  planar solve.
- Shift the piecewise residual cache with its LUT when removing a point.
- Replace the chroma solve fallback with explicit handling of exactly zero
  chroma residual. Other singular models fail.
- Reduce the normalized AR innovation-variance floor from 1e-6 to 1e-12 so
  fractional 10-bit residuals are not clamped into stronger grain.
- Remove chroma's luma-correlated energy using the variance of the same
  subsampled luma residual used by the AR fit, not full-resolution luma
  variance. A known 4:2:0 correlation fixture otherwise estimated independent
  chroma strength near 1.2 instead of 4 and clipped the cross-plane coefficient.
- Correct misleading argument names/order in the noise-variance wrapper
  without changing the order of values passed to its implementation.

`estimator_test.go` checks synthetic parameter recovery and rejection cases;
`internal/encode/grain_test.go` exercises generated tables through SVT. These
are correctness tests, not evidence of a real-title visual improvement. Timing
and viewing decisions belong in `docs/PERFORMANCE_TESTING.md`.
