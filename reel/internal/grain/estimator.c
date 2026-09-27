#include "estimator.h"
#include "noise_model.h"

#include <math.h>
#include <stdlib.h>
#include <string.h>

#define BLOCK 32
#define PIXELS (BLOCK * BLOCK)
#define PATCH_SAMPLES (PIXELS * 3 / 2)
#define CANDIDATES 128
#define MAX_PATCHES (REEL_GRAIN_MAX_FRAMES * REEL_GRAIN_PATCHES_PER_FRAME)

typedef struct {
  uint16_t source[PATCH_SAMPLES];
  uint16_t denoised[PATCH_SAMPLES];
} grain_patch;

struct reel_grain_estimator {
  reel_aom_flat_block_finder_t finder;
  int count;
  int frames;
  grain_patch patches[MAX_PATCHES];
};

typedef struct {
  int x, y;
  double score;
} candidate;

reel_grain_estimator *reel_grain_create(void) {
  reel_grain_estimator *e = calloc(1, sizeof(*e));
  if (!e) return NULL;
  if (!reel_aom_flat_block_finder_init(&e->finder, BLOCK, 10, 1)) {
    reel_grain_free(e);
    return NULL;
  }
  return e;
}

void reel_grain_free(reel_grain_estimator *e) {
  if (!e) return;
  reel_aom_flat_block_finder_free(&e->finder);
  free(e);
}

static uint16_t read_pixel(const uint8_t *p) {
  return (uint16_t)p[0] | ((uint16_t)p[1] << 8);
}

static void copy_patch(uint16_t *out, const uint8_t *in, int width, int height,
                       int x, int y) {
  for (int c = 0; c < 3; ++c) {
    const int sub = c > 0;
    const int size = BLOCK >> sub;
    const int stride = width >> sub;
    const int offset = c == 0 ? 0 : width * height + (c - 1) * width * height / 4;
    for (int j = 0; j < size; ++j) {
      for (int i = 0; i < size; ++i) {
        *out++ = read_pixel(in + 2 * (offset + ((y >> sub) + j) * stride + (x >> sub) + i));
      }
    }
  }
}

// Detrend the denoised patch using libaom's planar fit, then prefer patches
// whose remaining texture is small compared to the removed noise. Selecting
// only by residual energy would mistake blurred edges and lettering for grain.
static double patch_score(reel_grain_estimator *e, const grain_patch *p) {
  double plane[PIXELS], detail[PIXELS];
  double sum = 0, sum2 = 0;
  int clipped = 0;
  for (int i = 0; i < PIXELS; ++i) {
    const int s = p->source[i], d = p->denoised[i];
    if (s > 1023 || d > 1023) return -1;
    clipped += s <= 4 || s >= 1019 || d <= 4 || d >= 1019;
    const double r = s - d;
    sum += r;
    sum2 += r * r;
  }
  // Clipping biases both variance and correlation. Constant bars/patches have
  // no measurable grain and are not evidence for a title-wide model.
  if (clipped > PIXELS / 100) return -1;
  const double mean = sum / PIXELS;
  const double variance = sum2 / PIXELS - mean * mean;
  if (variance < 0.16 || fabs(mean) > fmax(1.0, sqrt(variance) / 4)) return -1;

  reel_aom_flat_block_finder_extract_block(&e->finder, (const uint8_t *)p->denoised,
                                          BLOCK, BLOCK, BLOCK, 0, 0, plane, detail);
  double detail_var = 0, gxx = 0, gyy = 0, gxy = 0;
  for (int y = 1; y < BLOCK - 1; ++y) {
    for (int x = 1; x < BLOCK - 1; ++x) {
      const int i = y * BLOCK + x;
      detail_var += detail[i] * detail[i] * 1023.0 * 1023.0;
      const double gx = (p->source[i+1] - p->denoised[i+1]) -
                        (p->source[i-1] - p->denoised[i-1]);
      const double gy = (p->source[i+BLOCK] - p->denoised[i+BLOCK]) -
                        (p->source[i-BLOCK] - p->denoised[i-BLOCK]);
      gxx += gx * gx;
      gyy += gy * gy;
      gxy += gx * gy;
    }
  }
  detail_var /= (BLOCK - 2) * (BLOCK - 2);
  const double trace = gxx + gyy;
  const double disc = sqrt(fmax(0, (gxx - gyy) * (gxx - gyy) + 4 * gxy * gxy));
  const double ratio = (trace + disc) / fmax(trace - disc, 1e-6);
  // Very directional residuals are usually structure, not stochastic grain.
  // These are conservative rejection guards, not a calibrated grain detector;
  // the bitrate gate still decides which titles need treatment.
  if (ratio > 4 || detail_var > 4 * variance) return -1;
  return detail_var / variance + (ratio - 1) / 4;
}

static int compare_candidates(const void *a, const void *b) {
  const candidate *x = a, *y = b;
  if (x->score != y->score) return x->score < y->score ? -1 : 1;
  // Stable tie breaking keeps the exact model independent of libc qsort.
  if (x->y != y->y) return x->y < y->y ? -1 : 1;
  return (x->x > y->x) - (x->x < y->x);
}

int reel_grain_sample(reel_grain_estimator *e, const uint8_t *source,
                      const uint8_t *denoised, int width, int height, int phase) {
  if (!e || e->frames >= REEL_GRAIN_MAX_FRAMES || width < BLOCK || height < BLOCK) return -1;
  ++e->frames;
  candidate candidates[CANDIDATES];
  int n = 0;
  // At most one native-resolution patch per spatial cell. Rotate its location
  // across sampled frames without scaling away the grain's spatial spectrum.
  const int nx = width / BLOCK < 16 ? width / BLOCK : 16;
  const int ny = height / BLOCK < 8 ? height / BLOCK : 8;
  grain_patch patch;
  for (int row = 0; row < ny; ++row) {
    for (int col = 0; col < nx; ++col) {
      const int left = col * width / nx, top = row * height / ny;
      const int dx = (col + 1) * width / nx - left - BLOCK;
      const int dy = (row + 1) * height / ny - top - BLOCK;
      const int x = (left + dx * ((phase + 1) % 5) / 4) & ~1;
      const int y = (top + dy * ((phase + 3) % 5) / 4) & ~1;
      copy_patch(patch.source, source, width, height, x, y);
      copy_patch(patch.denoised, denoised, width, height, x, y);
      const double score = patch_score(e, &patch);
      if (score >= 0) candidates[n++] = (candidate){x, y, score};
    }
  }
  qsort(candidates, n, sizeof(*candidates), compare_candidates);
  const int count = n < REEL_GRAIN_PATCHES_PER_FRAME ? n : REEL_GRAIN_PATCHES_PER_FRAME;
  for (int i = 0; i < count; ++i) {
    grain_patch *p = &e->patches[e->count++];
    copy_patch(p->source, source, width, height, candidates[i].x, candidates[i].y);
    copy_patch(p->denoised, denoised, width, height, candidates[i].x, candidates[i].y);
  }
  return count;
}

// Quantization must not turn a fitted AR filter into an unstable recursion.
// This is a bounded numerical sanity check, not another synthesis mechanism.
static int stable_ar(const int *coeffs, int shift, double expected_gain) {
  enum { SIZE = 128 };
  double *noise = calloc(SIZE * SIZE, sizeof(*noise));
  if (!noise) return 0;
  uint32_t seed = 1;
  double energy = 0;
  int samples = 0, ok = 1;
  for (int y = 3; y < SIZE && ok; ++y) {
    for (int x = 3; x < SIZE - 3; ++x) {
      seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5;
      double value = ((double)seed / 4294967295.0 * 2 - 1) * sqrt(3.0);
      int k = 0;
      for (int dy = -3; dy <= 0; ++dy) {
        for (int dx = -3; dx <= (dy == 0 ? -1 : 3); ++dx) {
          value += noise[(y + dy) * SIZE + x + dx] * coeffs[k++] / (1 << shift);
        }
      }
      if (!isfinite(value) || fabs(value) > 64) { ok = 0; break; }
      noise[y * SIZE + x] = value;
      if (y >= 32 && x >= 32 && x < SIZE - 16) { energy += value * value; ++samples; }
    }
  }
  const double gain2 = energy / (samples > 0 ? samples : 1);
  free(noise);
  return ok && gain2 > expected_gain * expected_gain / 2 && gain2 < expected_gain * expected_gain * 2;
}

int reel_grain_fit(reel_grain_estimator *e, reel_aom_film_grain_t *grain) {
  if (!e || e->count < 32) return 0;
  // Place each 32x32 patch in a 64x64 cell with unselected guard blocks.
  // The reference fitter consults that mask before using neighbouring blocks,
  // so no AR observation crosses a join between unrelated source patches.
  const int width = 16 * BLOCK * 2;
  const int height = ((e->count + 15) / 16) * BLOCK * 2;
  const int samples = width * height * 3 / 2;
  uint16_t *source = calloc(samples, sizeof(*source));
  uint16_t *denoised = calloc(samples, sizeof(*denoised));
  uint8_t *mask = calloc(width / BLOCK * height / BLOCK, 1);
  if (!source || !denoised || !mask) { free(source); free(denoised); free(mask); return 0; }
  for (int i = 0; i < e->count; ++i) {
    const int x = i % 16 * BLOCK * 2, y = i / 16 * BLOCK * 2;
    mask[y / BLOCK * (width / BLOCK) + x / BLOCK] = 1;
    int from = 0;
    for (int c = 0; c < 3; ++c) {
      const int sub = c > 0, stride = width >> sub, size = BLOCK >> sub;
      const int offset = c == 0 ? 0 : width * height + (c - 1) * width * height / 4;
      for (int row = 0; row < size; ++row) {
        const int dest = offset + ((y >> sub) + row) * stride + (x >> sub);
        memcpy(source + dest, e->patches[i].source + from, size * sizeof(*source));
        memcpy(denoised + dest, e->patches[i].denoised + from, size * sizeof(*denoised));
        from += size;
      }
    }
  }
  reel_aom_noise_model_t model;
  const reel_aom_noise_model_params_t params = {AOM_NOISE_SHAPE_SQUARE, 3, 10, 1};
  int ok = reel_aom_noise_model_init(&model, params);
  if (ok) {
    const uint8_t *src[3] = {(uint8_t *)source, (uint8_t *)(source + width * height),
                             (uint8_t *)(source + width * height * 5 / 4)};
    const uint8_t *dst[3] = {(uint8_t *)denoised, (uint8_t *)(denoised + width * height),
                             (uint8_t *)(denoised + width * height * 5 / 4)};
    int strides[3] = {width, width / 2, width / 2}, sub[2] = {1, 1};
    ok = reel_aom_noise_model_update(&model, src, dst, width, height, strides, sub, mask, BLOCK) == AOM_NOISE_STATUS_OK;
  }
  for (int c = 0; c < 3 && ok; ++c) {
    const reel_aom_noise_state_t *state = &model.combined_state[c];
    ok = isfinite(state->ar_gain) && state->ar_gain >= 1 && state->ar_gain <= 8;
    for (int i = 0; i < state->eqns.n && ok; ++i)
      ok = isfinite(state->eqns.x[i]) && fabs(state->eqns.x[i]) < 2;
    for (int i = 0; i < state->strength_solver.eqns.n && ok; ++i) {
      const double strength = state->strength_solver.eqns.x[i];
      ok = isfinite(strength) && strength >= 0 && strength < 128;
    }
  }
  if (ok) {
    memset(grain, 0, sizeof(*grain));
    grain->random_seed = 10956;
    ok = reel_aom_noise_model_get_grain_parameters(&model, grain);
  }
  if (ok) {
    int *coeffs[3] = {grain->ar_coeffs_y, grain->ar_coeffs_cb, grain->ar_coeffs_cr};
    int *counts[3] = {&grain->num_y_points, &grain->num_cb_points, &grain->num_cr_points};
    int (*points[3])[2] = {grain->scaling_points_y, grain->scaling_points_cb, grain->scaling_points_cr};
    for (int c = 0; c < 3 && ok; ++c) {
      int max_strength = 0;
      for (int i = 0; i < *counts[c]; ++i) {
        if (points[c][i][1] > max_strength) max_strength = points[c][i][1];
        if (i > 0 && points[c][i][0] <= points[c][i-1][0]) ok = 0;
      }
      if (max_strength == 0) {
        // Zero chroma residual is a valid monochrome model, not a failed fit.
        *counts[c] = 0;
        memset(coeffs[c], 0, (24 + (c > 0)) * sizeof(int));
        if (c == 0) ok = 0;
      } else {
        ok = ok && stable_ar(coeffs[c], grain->ar_coeff_shift, model.combined_state[c].ar_gain);
      }
    }
  }
  reel_aom_noise_model_free(&model);
  free(source); free(denoised); free(mask);
  return ok;
}
