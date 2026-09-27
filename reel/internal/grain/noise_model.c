/*
 * Copyright (c) 2017, Alliance for Open Media. All rights reserved.
 *
 * This source code is subject to the terms of the BSD 2 Clause License and
 * the Alliance for Open Media Patent License 1.0. If the BSD 2 Clause License
 * was not distributed with this source code in the LICENSE file, you can
 * obtain it at www.aomedia.org/license/software. If the Alliance for Open
 * Media Patent License 1.0 was not distributed with this source code in the
 * PATENTS file, you can obtain it at www.aomedia.org/license/patent.
 */

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Extracted from libaom 023743a7d78d9c7d33390c5c45b35495ea013a21.
// Reel supplies already-denoised, isolated patches; no libaom encoder or
// denoiser is linked. See UPSTREAM.md for local changes and licensing.
#include "noise_model.h"
#include "mathutils.h"
#include <assert.h>

#define AOMMIN(a, b) ((a) < (b) ? (a) : (b))
#define AOMMAX(a, b) ((a) > (b) ? (a) : (b))
#define reel_aom_malloc malloc
#define reel_aom_free free
static int clamp(int v, int lo, int hi) { return AOMMIN(AOMMAX(v, lo), hi); }
static double fclamp(double v, double lo, double hi) { return AOMMIN(AOMMAX(v, lo), hi); }

#define kLowPolyNumParams 3

static const int kMaxLag = 4;

// Defines a function that can be used to obtain the mean of a block for the
// provided data type (uint8_t, or uint16_t)
#define GET_BLOCK_MEAN(INT_TYPE, suffix)                                    \
  static double get_block_mean_##suffix(const INT_TYPE *data, int w, int h, \
                                        int stride, int x_o, int y_o,       \
                                        int block_size) {                   \
    const int max_h = AOMMIN(h - y_o, block_size);                          \
    const int max_w = AOMMIN(w - x_o, block_size);                          \
    double block_mean = 0;                                                  \
    for (int y = 0; y < max_h; ++y) {                                       \
      for (int x = 0; x < max_w; ++x) {                                     \
        block_mean += data[(y_o + y) * stride + x_o + x];                   \
      }                                                                     \
    }                                                                       \
    return block_mean / (max_w * max_h);                                    \
  }

GET_BLOCK_MEAN(uint8_t, lowbd)
GET_BLOCK_MEAN(uint16_t, highbd)

static inline double get_block_mean(const uint8_t *data, int w, int h,
                                    int stride, int x_o, int y_o,
                                    int block_size, int use_highbd) {
  if (use_highbd)
    return get_block_mean_highbd((const uint16_t *)data, w, h, stride, x_o, y_o,
                                 block_size);
  return get_block_mean_lowbd(data, w, h, stride, x_o, y_o, block_size);
}

// Defines a function that can be used to obtain the variance of a block
// for the provided data type (uint8_t, or uint16_t)
#define GET_NOISE_VAR(INT_TYPE, suffix)                                  \
  static double get_noise_var_##suffix(                                  \
      const INT_TYPE *data, const INT_TYPE *denoised, int stride, int w, \
      int h, int x_o, int y_o, int block_size_x, int block_size_y) {     \
    const int max_h = AOMMIN(h - y_o, block_size_y);                     \
    const int max_w = AOMMIN(w - x_o, block_size_x);                     \
    double noise_var = 0;                                                \
    double noise_mean = 0;                                               \
    for (int y = 0; y < max_h; ++y) {                                    \
      for (int x = 0; x < max_w; ++x) {                                  \
        double noise = (double)data[(y_o + y) * stride + x_o + x] -      \
                       denoised[(y_o + y) * stride + x_o + x];           \
        noise_mean += noise;                                             \
        noise_var += noise * noise;                                      \
      }                                                                  \
    }                                                                    \
    noise_mean /= (max_w * max_h);                                       \
    return noise_var / (max_w * max_h) - noise_mean * noise_mean;        \
  }

GET_NOISE_VAR(uint8_t, lowbd)
GET_NOISE_VAR(uint16_t, highbd)

static inline double get_noise_var(const uint8_t *data, const uint8_t *denoised,
                                   int stride, int w, int h, int x_o, int y_o,
                                   int block_size_x, int block_size_y,
                                   int use_highbd) {
  if (use_highbd)
    return get_noise_var_highbd((const uint16_t *)data,
                                (const uint16_t *)denoised, stride, w, h, x_o,
                                y_o, block_size_x, block_size_y);
  return get_noise_var_lowbd(data, denoised, stride, w, h, x_o, y_o,
                             block_size_x, block_size_y);
}

static void equation_system_clear(reel_aom_equation_system_t *eqns) {
  const int n = eqns->n;
  memset(eqns->A, 0, sizeof(*eqns->A) * n * n);
  memset(eqns->x, 0, sizeof(*eqns->x) * n);
  memset(eqns->b, 0, sizeof(*eqns->b) * n);
}

static int equation_system_init(reel_aom_equation_system_t *eqns, int n) {
  eqns->A = (double *)reel_aom_malloc(sizeof(*eqns->A) * n * n);
  eqns->b = (double *)reel_aom_malloc(sizeof(*eqns->b) * n);
  eqns->x = (double *)reel_aom_malloc(sizeof(*eqns->x) * n);
  eqns->n = n;
  if (!eqns->A || !eqns->b || !eqns->x) {
    reel_aom_free(eqns->A);
    reel_aom_free(eqns->b);
    reel_aom_free(eqns->x);
    memset(eqns, 0, sizeof(*eqns));
    return 0;
  }
  equation_system_clear(eqns);
  return 1;
}

static int equation_system_solve(reel_aom_equation_system_t *eqns) {
  const int n = eqns->n;
  double *b = (double *)reel_aom_malloc(sizeof(*b) * n);
  double *A = (double *)reel_aom_malloc(sizeof(*A) * n * n);
  int ret = 0;
  if (A == NULL || b == NULL) {
    reel_aom_free(b);
    reel_aom_free(A);
    return 0;
  }
  memcpy(A, eqns->A, sizeof(*eqns->A) * n * n);
  memcpy(b, eqns->b, sizeof(*eqns->b) * n);
  ret = linsolve(n, A, eqns->n, b, eqns->x);
  reel_aom_free(b);
  reel_aom_free(A);

  if (ret == 0) {
    return 0;
  }
  for (int i = 0; i < n; ++i) {
    if (!isfinite(eqns->x[i])) {
      return 0;
    }
  }
  return 1;
}

static void equation_system_add(reel_aom_equation_system_t *dest,
                                reel_aom_equation_system_t *src) {
  const int n = dest->n;
  int i, j;
  for (i = 0; i < n; ++i) {
    for (j = 0; j < n; ++j) {
      dest->A[i * n + j] += src->A[i * n + j];
    }
    dest->b[i] += src->b[i];
  }
}

static void equation_system_free(reel_aom_equation_system_t *eqns) {
  if (!eqns) return;
  reel_aom_free(eqns->A);
  reel_aom_free(eqns->b);
  reel_aom_free(eqns->x);
  memset(eqns, 0, sizeof(*eqns));
}

static void noise_strength_solver_clear(reel_aom_noise_strength_solver_t *solver) {
  equation_system_clear(&solver->eqns);
  solver->num_equations = 0;
  solver->total = 0;
}

static void noise_strength_solver_add(reel_aom_noise_strength_solver_t *dest,
                                      reel_aom_noise_strength_solver_t *src) {
  equation_system_add(&dest->eqns, &src->eqns);
  dest->num_equations += src->num_equations;
  dest->total += src->total;
}

// Return the number of coefficients required for the given parameters
static int num_coeffs(const reel_aom_noise_model_params_t params) {
  const int n = 2 * params.lag + 1;
  switch (params.shape) {
    case AOM_NOISE_SHAPE_DIAMOND: return params.lag * (params.lag + 1);
    case AOM_NOISE_SHAPE_SQUARE: return (n * n) / 2;
  }
  return 0;
}

static int noise_state_init(reel_aom_noise_state_t *state, int n, int bit_depth) {
  const int kNumBins = 20;
  if (!equation_system_init(&state->eqns, n)) {
    return 0;
  }
  state->ar_gain = 1.0;
  state->num_observations = 0;
  return reel_aom_noise_strength_solver_init(&state->strength_solver, kNumBins,
                                        bit_depth);
}

int reel_aom_noise_strength_lut_init(reel_aom_noise_strength_lut_t *lut, int num_points) {
  if (!lut) return 0;
  if (num_points <= 0) return 0;
  lut->num_points = 0;
  lut->points = (double(*)[2])reel_aom_malloc(num_points * sizeof(*lut->points));
  if (!lut->points) return 0;
  lut->num_points = num_points;
  memset(lut->points, 0, sizeof(*lut->points) * num_points);
  return 1;
}

void reel_aom_noise_strength_lut_free(reel_aom_noise_strength_lut_t *lut) {
  if (!lut) return;
  reel_aom_free(lut->points);
  memset(lut, 0, sizeof(*lut));
}

double reel_aom_noise_strength_lut_eval(const reel_aom_noise_strength_lut_t *lut,
                                   double x) {
  int i = 0;
  // Constant extrapolation for x <  x_0.
  if (x < lut->points[0][0]) return lut->points[0][1];
  for (i = 0; i < lut->num_points - 1; ++i) {
    if (x >= lut->points[i][0] && x <= lut->points[i + 1][0]) {
      const double a =
          (x - lut->points[i][0]) / (lut->points[i + 1][0] - lut->points[i][0]);
      return lut->points[i + 1][1] * a + lut->points[i][1] * (1.0 - a);
    }
  }
  // Constant extrapolation for x > x_{n-1}
  return lut->points[lut->num_points - 1][1];
}

static double noise_strength_solver_get_bin_index(
    const reel_aom_noise_strength_solver_t *solver, double value) {
  const double val =
      fclamp(value, solver->min_intensity, solver->max_intensity);
  const double range = solver->max_intensity - solver->min_intensity;
  return (solver->num_bins - 1) * (val - solver->min_intensity) / range;
}

void reel_aom_noise_strength_solver_add_measurement(
    reel_aom_noise_strength_solver_t *solver, double block_mean, double noise_std) {
  const double bin = noise_strength_solver_get_bin_index(solver, block_mean);
  const int bin_i0 = (int)floor(bin);
  const int bin_i1 = AOMMIN(solver->num_bins - 1, bin_i0 + 1);
  const double a = bin - bin_i0;
  const int n = solver->num_bins;
  solver->eqns.A[bin_i0 * n + bin_i0] += (1.0 - a) * (1.0 - a);
  solver->eqns.A[bin_i1 * n + bin_i0] += a * (1.0 - a);
  solver->eqns.A[bin_i1 * n + bin_i1] += a * a;
  solver->eqns.A[bin_i0 * n + bin_i1] += a * (1.0 - a);
  solver->eqns.b[bin_i0] += (1.0 - a) * noise_std;
  solver->eqns.b[bin_i1] += a * noise_std;
  solver->total += noise_std;
  solver->num_equations++;
}

int reel_aom_noise_strength_solver_solve(reel_aom_noise_strength_solver_t *solver) {
  if (solver->num_equations == 0) return 0;
  // Add regularization proportional to the number of constraints
  const int n = solver->num_bins;
  const double kAlpha = 2.0 * (double)(solver->num_equations) / n;
  int result = 0;
  double mean = 0;

  // Do this in a non-destructive manner so it is not confusing to the caller
  double *old_A = solver->eqns.A;
  double *A = (double *)reel_aom_malloc(sizeof(*A) * n * n);
  if (!A) {
    return 0;
  }
  memcpy(A, old_A, sizeof(*A) * n * n);

  for (int i = 0; i < n; ++i) {
    const int i_lo = AOMMAX(0, i - 1);
    const int i_hi = AOMMIN(n - 1, i + 1);
    A[i * n + i_lo] -= kAlpha;
    A[i * n + i] += 2 * kAlpha;
    A[i * n + i_hi] -= kAlpha;
  }

  // Small regularization to give average noise strength
  mean = solver->total / solver->num_equations;
  for (int i = 0; i < n; ++i) {
    A[i * n + i] += 1.0 / 8192.;
    solver->eqns.b[i] += mean / 8192.;
  }
  solver->eqns.A = A;
  result = equation_system_solve(&solver->eqns);
  solver->eqns.A = old_A;
  for (int i = 0; i < n; ++i) solver->eqns.b[i] -= mean / 8192.;

  reel_aom_free(A);
  return result;
}

int reel_aom_noise_strength_solver_init(reel_aom_noise_strength_solver_t *solver,
                                   int num_bins, int bit_depth) {
  if (!solver) return 0;
  memset(solver, 0, sizeof(*solver));
  solver->num_bins = num_bins;
  solver->min_intensity = 0;
  solver->max_intensity = (1 << bit_depth) - 1;
  solver->total = 0;
  solver->num_equations = 0;
  return equation_system_init(&solver->eqns, num_bins);
}

void reel_aom_noise_strength_solver_free(reel_aom_noise_strength_solver_t *solver) {
  if (!solver) return;
  equation_system_free(&solver->eqns);
}

double reel_aom_noise_strength_solver_get_center(
    const reel_aom_noise_strength_solver_t *solver, int i) {
  const double range = solver->max_intensity - solver->min_intensity;
  const int n = solver->num_bins;
  return ((double)i) / (n - 1) * range + solver->min_intensity;
}

// Computes the residual if a point were to be removed from the lut. This is
// calculated as the area between the output of the solver and the line segment
// that would be formed between [x_{i - 1}, x_{i + 1}).
static void update_piecewise_linear_residual(
    const reel_aom_noise_strength_solver_t *solver,
    const reel_aom_noise_strength_lut_t *lut, double *residual, int start, int end) {
  const double dx = 255. / solver->num_bins;
  for (int i = AOMMAX(start, 1); i < AOMMIN(end, lut->num_points - 1); ++i) {
    const int lower = AOMMAX(0, (int)floor(noise_strength_solver_get_bin_index(
                                    solver, lut->points[i - 1][0])));
    const int upper = AOMMIN(solver->num_bins - 1,
                             (int)ceil(noise_strength_solver_get_bin_index(
                                 solver, lut->points[i + 1][0])));
    double r = 0;
    for (int j = lower; j <= upper; ++j) {
      const double x = reel_aom_noise_strength_solver_get_center(solver, j);
      if (x < lut->points[i - 1][0]) continue;
      if (x >= lut->points[i + 1][0]) continue;
      const double y = solver->eqns.x[j];
      const double a = (x - lut->points[i - 1][0]) /
                       (lut->points[i + 1][0] - lut->points[i - 1][0]);
      const double estimate_y =
          lut->points[i - 1][1] * (1.0 - a) + lut->points[i + 1][1] * a;
      r += fabs(y - estimate_y);
    }
    residual[i] = r * dx;
  }
}

int reel_aom_noise_strength_solver_fit_piecewise(
    const reel_aom_noise_strength_solver_t *solver, int max_output_points,
    reel_aom_noise_strength_lut_t *lut) {
  // The tolerance is normalized to be give consistent results between
  // different bit-depths.
  const double kTolerance = solver->max_intensity * 0.00625 / 255.0;
  if (!reel_aom_noise_strength_lut_init(lut, solver->num_bins)) {
    return 0;
  }
  for (int i = 0; i < solver->num_bins; ++i) {
    lut->points[i][0] = reel_aom_noise_strength_solver_get_center(solver, i);
    lut->points[i][1] = solver->eqns.x[i];
  }
  if (max_output_points < 0) {
    max_output_points = solver->num_bins;
  }

  double *residual = (double *)reel_aom_malloc(solver->num_bins * sizeof(*residual));
  if (!residual) {
    reel_aom_noise_strength_lut_free(lut);
    return 0;
  }
  memset(residual, 0, sizeof(*residual) * solver->num_bins);

  update_piecewise_linear_residual(solver, lut, residual, 0, solver->num_bins);

  // Greedily remove points if there are too many or if it doesn't hurt local
  // approximation (never remove the end points)
  while (lut->num_points > 2) {
    int min_index = 1;
    for (int j = 1; j < lut->num_points - 1; ++j) {
      if (residual[j] < residual[min_index]) {
        min_index = j;
      }
    }
    const double dx =
        lut->points[min_index + 1][0] - lut->points[min_index - 1][0];
    const double avg_residual = residual[min_index] / dx;
    if (lut->num_points <= max_output_points && avg_residual > kTolerance) {
      break;
    }

    const int num_remaining = lut->num_points - min_index - 1;
    memmove(lut->points + min_index, lut->points + min_index + 1,
            sizeof(lut->points[0]) * num_remaining);
    memmove(residual + min_index, residual + min_index + 1,
            sizeof(*residual) * num_remaining);
    lut->num_points--;

    update_piecewise_linear_residual(solver, lut, residual, min_index - 1,
                                     min_index + 1);
  }
  reel_aom_free(residual);
  return 1;
}

int reel_aom_flat_block_finder_init(reel_aom_flat_block_finder_t *block_finder,
                               int block_size, int bit_depth, int use_highbd) {
  const int n = block_size * block_size;
  reel_aom_equation_system_t eqns;
  double *AtA_inv = 0;
  double *A = 0;
  int x = 0, y = 0, i = 0, j = 0;
  block_finder->A = NULL;
  block_finder->AtA_inv = NULL;

  if (!equation_system_init(&eqns, kLowPolyNumParams)) {
    return 0;
  }

  AtA_inv = (double *)reel_aom_malloc(kLowPolyNumParams * kLowPolyNumParams *
                                 sizeof(*AtA_inv));
  A = (double *)reel_aom_malloc(kLowPolyNumParams * n * sizeof(*A));
  if (AtA_inv == NULL || A == NULL) {
    reel_aom_free(AtA_inv);
    reel_aom_free(A);
    equation_system_free(&eqns);
    return 0;
  }

  block_finder->A = A;
  block_finder->AtA_inv = AtA_inv;
  block_finder->block_size = block_size;
  block_finder->normalization = (1 << bit_depth) - 1;
  block_finder->use_highbd = use_highbd;

  for (y = 0; y < block_size; ++y) {
    const double yd = ((double)y - block_size / 2.) / (block_size / 2.);
    for (x = 0; x < block_size; ++x) {
      const double xd = ((double)x - block_size / 2.) / (block_size / 2.);
      const double coords[3] = { yd, xd, 1 };
      const int row = y * block_size + x;
      A[kLowPolyNumParams * row + 0] = yd;
      A[kLowPolyNumParams * row + 1] = xd;
      A[kLowPolyNumParams * row + 2] = 1;

      for (i = 0; i < kLowPolyNumParams; ++i) {
        for (j = 0; j < kLowPolyNumParams; ++j) {
          eqns.A[kLowPolyNumParams * i + j] += coords[i] * coords[j];
        }
      }
    }
  }

  // Lazy inverse using existing equation solver.
  for (i = 0; i < kLowPolyNumParams; ++i) {
    memset(eqns.b, 0, sizeof(*eqns.b) * kLowPolyNumParams);
    eqns.b[i] = 1;
    const int ret = equation_system_solve(&eqns);
    if (!ret) {
      equation_system_free(&eqns);
      return ret;
    }

    for (j = 0; j < kLowPolyNumParams; ++j) {
      AtA_inv[j * kLowPolyNumParams + i] = eqns.x[j];
    }
  }
  equation_system_free(&eqns);
  return 1;
}

void reel_aom_flat_block_finder_free(reel_aom_flat_block_finder_t *block_finder) {
  if (!block_finder) return;
  reel_aom_free(block_finder->A);
  reel_aom_free(block_finder->AtA_inv);
  memset(block_finder, 0, sizeof(*block_finder));
}

void reel_aom_flat_block_finder_extract_block(
    const reel_aom_flat_block_finder_t *block_finder, const uint8_t *const data,
    int w, int h, int stride, int offsx, int offsy, double *plane,
    double *block) {
  const int block_size = block_finder->block_size;
  const int n = block_size * block_size;
  const double *A = block_finder->A;
  const double *AtA_inv = block_finder->AtA_inv;
  double plane_coords[kLowPolyNumParams];
  double AtA_inv_b[kLowPolyNumParams];
  int xi, yi, i;

  if (block_finder->use_highbd) {
    const uint16_t *const data16 = (const uint16_t *const)data;
    for (yi = 0; yi < block_size; ++yi) {
      const int y = clamp(offsy + yi, 0, h - 1);
      for (xi = 0; xi < block_size; ++xi) {
        const int x = clamp(offsx + xi, 0, w - 1);
        block[yi * block_size + xi] =
            ((double)data16[y * stride + x]) / block_finder->normalization;
      }
    }
  } else {
    for (yi = 0; yi < block_size; ++yi) {
      const int y = clamp(offsy + yi, 0, h - 1);
      for (xi = 0; xi < block_size; ++xi) {
        const int x = clamp(offsx + xi, 0, w - 1);
        block[yi * block_size + xi] =
            ((double)data[y * stride + x]) / block_finder->normalization;
      }
    }
  }
  multiply_mat(block, A, AtA_inv_b, 1, n, kLowPolyNumParams);
  multiply_mat(AtA_inv, AtA_inv_b, plane_coords, kLowPolyNumParams,
               kLowPolyNumParams, 1);
  multiply_mat(A, plane_coords, plane, n, kLowPolyNumParams, 1);

  for (i = 0; i < n; ++i) {
    block[i] -= plane[i];
  }
}

int reel_aom_noise_model_init(reel_aom_noise_model_t *model,
                         const reel_aom_noise_model_params_t params) {
  const int n = num_coeffs(params);
  const int lag = params.lag;
  const int bit_depth = params.bit_depth;
  int x = 0, y = 0, i = 0, c = 0;

  memset(model, 0, sizeof(*model));
  if (params.lag < 1) {
    return 0;
  }
  if (params.lag > kMaxLag) {
    return 0;
  }
  if (!(params.bit_depth == 8 || params.bit_depth == 10 ||
        params.bit_depth == 12)) {
    return 0;
  }

  model->params = params;
  for (c = 0; c < 3; ++c) {
    if (!noise_state_init(&model->combined_state[c], n + (c > 0), bit_depth)) {
      reel_aom_noise_model_free(model);
      return 0;
    }
    if (!noise_state_init(&model->latest_state[c], n + (c > 0), bit_depth)) {
      reel_aom_noise_model_free(model);
      return 0;
    }
  }
  model->n = n;
  model->coords = (int(*)[2])reel_aom_malloc(sizeof(*model->coords) * n);
  if (!model->coords) {
    reel_aom_noise_model_free(model);
    return 0;
  }

  for (y = -lag; y <= 0; ++y) {
    const int max_x = y == 0 ? -1 : lag;
    for (x = -lag; x <= max_x; ++x) {
      switch (params.shape) {
        case AOM_NOISE_SHAPE_DIAMOND:
          if (abs(x) <= y + lag) {
            model->coords[i][0] = x;
            model->coords[i][1] = y;
            ++i;
          }
          break;
        case AOM_NOISE_SHAPE_SQUARE:
          model->coords[i][0] = x;
          model->coords[i][1] = y;
          ++i;
          break;
        default:
          reel_aom_noise_model_free(model);
          return 0;
      }
    }
  }
  assert(i == n);
  return 1;
}

void reel_aom_noise_model_free(reel_aom_noise_model_t *model) {
  int c = 0;
  if (!model) return;

  reel_aom_free(model->coords);
  for (c = 0; c < 3; ++c) {
    equation_system_free(&model->latest_state[c].eqns);
    equation_system_free(&model->combined_state[c].eqns);

    equation_system_free(&model->latest_state[c].strength_solver.eqns);
    equation_system_free(&model->combined_state[c].strength_solver.eqns);
  }
  memset(model, 0, sizeof(*model));
}

// Extracts the neighborhood defined by coords around point (x, y) from
// the difference between the data and denoised images. Also extracts the
// entry (possibly downsampled) for (x, y) in the alt_data (e.g., luma).
#define EXTRACT_AR_ROW(INT_TYPE, suffix)                                   \
  static double extract_ar_row_##suffix(                                   \
      int(*coords)[2], int num_coords, const INT_TYPE *const data,         \
      const INT_TYPE *const denoised, int stride, int sub_log2[2],         \
      const INT_TYPE *const alt_data, const INT_TYPE *const alt_denoised,  \
      int alt_stride, int x, int y, double *buffer) {                      \
    for (int i = 0; i < num_coords; ++i) {                                 \
      const int x_i = x + coords[i][0], y_i = y + coords[i][1];            \
      buffer[i] =                                                          \
          (double)data[y_i * stride + x_i] - denoised[y_i * stride + x_i]; \
    }                                                                      \
    const double val =                                                     \
        (double)data[y * stride + x] - denoised[y * stride + x];           \
                                                                           \
    if (alt_data && alt_denoised) {                                        \
      double avg_data = 0, avg_denoised = 0;                               \
      int num_samples = 0;                                                 \
      for (int dy_i = 0; dy_i < (1 << sub_log2[1]); dy_i++) {              \
        const int y_up = (y << sub_log2[1]) + dy_i;                        \
        for (int dx_i = 0; dx_i < (1 << sub_log2[0]); dx_i++) {            \
          const int x_up = (x << sub_log2[0]) + dx_i;                      \
          avg_data += alt_data[y_up * alt_stride + x_up];                  \
          avg_denoised += alt_denoised[y_up * alt_stride + x_up];          \
          num_samples++;                                                   \
        }                                                                  \
      }                                                                    \
      buffer[num_coords] = (avg_data - avg_denoised) / num_samples;        \
    }                                                                      \
    return val;                                                            \
  }

EXTRACT_AR_ROW(uint8_t, lowbd)
EXTRACT_AR_ROW(uint16_t, highbd)

static int add_block_observations(
    reel_aom_noise_model_t *noise_model, int c, const uint8_t *const data,
    const uint8_t *const denoised, int w, int h, int stride, int sub_log2[2],
    const uint8_t *const alt_data, const uint8_t *const alt_denoised,
    int alt_stride, const uint8_t *const flat_blocks, int block_size,
    int num_blocks_w, int num_blocks_h) {
  const int lag = noise_model->params.lag;
  const int num_coords = noise_model->n;
  const double normalization = (1 << noise_model->params.bit_depth) - 1;
  double *A = noise_model->latest_state[c].eqns.A;
  double *b = noise_model->latest_state[c].eqns.b;
  double *buffer = (double *)reel_aom_malloc(sizeof(*buffer) * (num_coords + 1));
  const int n = noise_model->latest_state[c].eqns.n;

  if (!buffer) {
    return 0;
  }
  for (int by = 0; by < num_blocks_h; ++by) {
    const int y_o = by * (block_size >> sub_log2[1]);
    for (int bx = 0; bx < num_blocks_w; ++bx) {
      const int x_o = bx * (block_size >> sub_log2[0]);
      if (!flat_blocks[by * num_blocks_w + bx]) {
        continue;
      }
      int y_start =
          (by > 0 && flat_blocks[(by - 1) * num_blocks_w + bx]) ? 0 : lag;
      int x_start =
          (bx > 0 && flat_blocks[by * num_blocks_w + bx - 1]) ? 0 : lag;
      int y_end = AOMMIN((h >> sub_log2[1]) - by * (block_size >> sub_log2[1]),
                         block_size >> sub_log2[1]);
      int x_end = AOMMIN(
          (w >> sub_log2[0]) - bx * (block_size >> sub_log2[0]) - lag,
          (bx + 1 < num_blocks_w && flat_blocks[by * num_blocks_w + bx + 1])
              ? (block_size >> sub_log2[0])
              : ((block_size >> sub_log2[0]) - lag));
      for (int y = y_start; y < y_end; ++y) {
        for (int x = x_start; x < x_end; ++x) {
          const double val =
              noise_model->params.use_highbd
                  ? extract_ar_row_highbd(noise_model->coords, num_coords,
                                          (const uint16_t *const)data,
                                          (const uint16_t *const)denoised,
                                          stride, sub_log2,
                                          (const uint16_t *const)alt_data,
                                          (const uint16_t *const)alt_denoised,
                                          alt_stride, x + x_o, y + y_o, buffer)
                  : extract_ar_row_lowbd(noise_model->coords, num_coords, data,
                                         denoised, stride, sub_log2, alt_data,
                                         alt_denoised, alt_stride, x + x_o,
                                         y + y_o, buffer);
          for (int i = 0; i < n; ++i) {
            for (int j = 0; j < n; ++j) {
              A[i * n + j] +=
                  (buffer[i] * buffer[j]) / (normalization * normalization);
            }
            b[i] += (buffer[i] * val) / (normalization * normalization);
          }
          noise_model->latest_state[c].num_observations++;
        }
      }
    }
  }
  reel_aom_free(buffer);
  return 1;
}

// The chroma AR row uses the subsampled (averaged) luma residual. Its
// contribution must be removed using that same residual's variance, not the
// full-resolution luma variance: averaging 2x2 independent samples quarters
// variance. Using the latter over-subtracts chroma energy and can saturate the
// signalled cross-plane coefficient even for an otherwise representable model.
static double subsampled_luma_variance(
    const uint8_t *data, const uint8_t *denoised, int stride,
    int x_o, int y_o, int w, int h, const int sub[2], int highbd) {
  double mean = 0, var = 0;
  for (int y = 0; y < h; ++y) {
    for (int x = 0; x < w; ++x) {
      double residual = 0;
      for (int dy = 0; dy < (1 << sub[1]); ++dy) {
        for (int dx = 0; dx < (1 << sub[0]); ++dx) {
          const int i = (((y_o + y) << sub[1]) + dy) * stride +
                        ((x_o + x) << sub[0]) + dx;
          residual += highbd ? (double)((const uint16_t *)data)[i] - ((const uint16_t *)denoised)[i]
                             : (double)data[i] - denoised[i];
        }
      }
      residual /= 1 << (sub[0] + sub[1]);
      mean += residual;
      var += residual * residual;
    }
  }
  mean /= w * h;
  return AOMMAX(0, var / (w * h) - mean * mean);
}

static void add_noise_std_observations(
    reel_aom_noise_model_t *noise_model, int c, const double *coeffs,
    const uint8_t *const data, const uint8_t *const denoised, int w, int h,
    int stride, int sub_log2[2], const uint8_t *const alt_data,
    const uint8_t *const alt_denoised, int alt_stride,
    const uint8_t *const flat_blocks, int block_size, int num_blocks_w,
    int num_blocks_h) {
  const int num_coords = noise_model->n;
  reel_aom_noise_strength_solver_t *noise_strength_solver =
      &noise_model->latest_state[c].strength_solver;

  const double noise_gain = noise_model->latest_state[c].ar_gain;
  for (int by = 0; by < num_blocks_h; ++by) {
    const int y_o = by * (block_size >> sub_log2[1]);
    for (int bx = 0; bx < num_blocks_w; ++bx) {
      const int x_o = bx * (block_size >> sub_log2[0]);
      if (!flat_blocks[by * num_blocks_w + bx]) {
        continue;
      }
      const int num_samples_h =
          AOMMIN((h >> sub_log2[1]) - by * (block_size >> sub_log2[1]),
                 block_size >> sub_log2[1]);
      const int num_samples_w =
          AOMMIN((w >> sub_log2[0]) - bx * (block_size >> sub_log2[0]),
                 (block_size >> sub_log2[0]));
      // Make sure that we have a reasonable amount of samples to consider the
      // block
      if (num_samples_w * num_samples_h > block_size) {
        const double block_mean = get_block_mean(
            alt_data ? alt_data : data, w, h, alt_data ? alt_stride : stride,
            x_o << sub_log2[0], y_o << sub_log2[1], block_size,
            noise_model->params.use_highbd);
        const double noise_var = get_noise_var(
            data, denoised, stride, w >> sub_log2[0], h >> sub_log2[1], x_o,
            y_o, block_size >> sub_log2[0], block_size >> sub_log2[1],
            noise_model->params.use_highbd);
        const double luma_var = c > 0 ? subsampled_luma_variance(
            alt_data, alt_denoised, alt_stride, x_o, y_o, num_samples_w,
            num_samples_h, sub_log2, noise_model->params.use_highbd) : 0;
        const double corr = c > 0 ? coeffs[num_coords] : 0;
        // Remove luma-correlated energy before fitting the independent chroma
        // strength. AV1 cannot model completely correlated noise.
        const double uncorr_std = sqrt(
            AOMMAX(noise_var / 16, noise_var - corr * corr * luma_var));
        // After we've removed correlation with luma, undo the gain that will
        // come from running the IIR filter.
        const double adjusted_strength = uncorr_std / noise_gain;
        reel_aom_noise_strength_solver_add_measurement(
            noise_strength_solver, block_mean, adjusted_strength);
      }
    }
  }
}

static int ar_equation_system_solve(reel_aom_noise_state_t *state, int is_chroma) {
  state->ar_gain = 1.0;
  if (state->num_observations == 0) return 0;
  // Exactly zero chroma residual is a valid no-chroma model. Other singular
  // fits are errors, not a reason to replace measured texture with white noise.
  if (is_chroma) {
    double energy = 0;
    for (int i = 0; i < state->eqns.n - 1; ++i)
      energy += state->eqns.A[i * state->eqns.n + i];
    if (energy == 0) {
      memset(state->eqns.x, 0, state->eqns.n * sizeof(*state->eqns.x));
      return 1;
    }
  }
  const int ret = equation_system_solve(&state->eqns);
  if (!ret) return ret;

  // Update the AR gain from the equation system as it will be used to fit
  // the noise strength as a function of intensity.  In the Yule-Walker
  // equations, the diagonal should be the variance of the correlated noise.
  // In the case of the least squares estimate, there will be some variability
  // in the diagonal. So use the mean of the diagonal as the estimate of
  // overall variance (this works for least squares or Yule-Walker formulation).
  double var = 0;
  const int n = state->eqns.n;
  for (int i = 0; i < (state->eqns.n - is_chroma); ++i) {
    var += state->eqns.A[i * n + i] / state->num_observations;
  }
  var /= (n - is_chroma);

  // Keep track of E(Y^2) = <b, x> + E(X^2)
  // In the case that we are using chroma and have an estimate of correlation
  // with luma we adjust that estimate slightly to remove the correlated bits by
  // subtracting out the last column of a scaled by our correlation estimate
  // from b. E(y^2) = <b - A(:, end)*x(end), x>
  double sum_covar = 0;
  for (int i = 0; i < state->eqns.n - is_chroma; ++i) {
    double bi = state->eqns.b[i];
    if (is_chroma) {
      bi -= state->eqns.A[i * n + (n - 1)] * state->eqns.x[n - 1];
    }
    sum_covar += (bi * state->eqns.x[i]) / state->num_observations;
  }
  // Now, get an estimate of the variance of uncorrelated noise signal and use
  // it to determine the gain of the AR filter.
  // Keep native 10-bit residuals: 1e-6 in normalized units would clamp
  // sub-8-bit grain and overestimate its innovation strength.
  const double noise_var = AOMMAX(var - sum_covar, 1e-12);
  state->ar_gain = AOMMAX(1, sqrt(AOMMAX(var / noise_var, 1e-6)));
  return ret;
}

reel_aom_noise_status_t reel_aom_noise_model_update(
    reel_aom_noise_model_t *const noise_model, const uint8_t *const data[3],
    const uint8_t *const denoised[3], int w, int h, int stride[3],
    int chroma_sub_log2[2], const uint8_t *const flat_blocks, int block_size) {
  const int num_blocks_w = (w + block_size - 1) / block_size;
  const int num_blocks_h = (h + block_size - 1) / block_size;
  int num_blocks = 0;
  int i = 0, channel = 0;

  if (block_size <= 1) {
    return AOM_NOISE_STATUS_INVALID_ARGUMENT;
  }

  if (block_size < noise_model->params.lag * 2 + 1) {
    return AOM_NOISE_STATUS_INVALID_ARGUMENT;
  }

  // Clear the latest equation system
  for (i = 0; i < 3; ++i) {
    equation_system_clear(&noise_model->latest_state[i].eqns);
    noise_model->latest_state[i].num_observations = 0;
    noise_strength_solver_clear(&noise_model->latest_state[i].strength_solver);
  }

  // Check that we have enough flat blocks
  for (i = 0; i < num_blocks_h * num_blocks_w; ++i) {
    if (flat_blocks[i]) {
      num_blocks++;
    }
  }

  if (num_blocks <= 1) {
    return AOM_NOISE_STATUS_INSUFFICIENT_FLAT_BLOCKS;
  }

  for (channel = 0; channel < 3; ++channel) {
    int no_subsampling[2] = { 0, 0 };
    const uint8_t *alt_data = channel > 0 ? data[0] : 0;
    const uint8_t *alt_denoised = channel > 0 ? denoised[0] : 0;
    int *sub = channel > 0 ? chroma_sub_log2 : no_subsampling;
    const int is_chroma = channel != 0;
    if (!data[channel] || !denoised[channel]) break;
    if (!add_block_observations(noise_model, channel, data[channel],
                                denoised[channel], w, h, stride[channel], sub,
                                alt_data, alt_denoised, stride[0], flat_blocks,
                                block_size, num_blocks_w, num_blocks_h)) {
      return AOM_NOISE_STATUS_INTERNAL_ERROR;
    }

    if (!ar_equation_system_solve(&noise_model->latest_state[channel], is_chroma))
      return AOM_NOISE_STATUS_INTERNAL_ERROR;

    add_noise_std_observations(
        noise_model, channel, noise_model->latest_state[channel].eqns.x,
        data[channel], denoised[channel], w, h, stride[channel], sub, alt_data,
        alt_denoised, stride[0], flat_blocks, block_size, num_blocks_w, num_blocks_h);

    if (!reel_aom_noise_strength_solver_solve(
            &noise_model->latest_state[channel].strength_solver)) {
      return AOM_NOISE_STATUS_INTERNAL_ERROR;
    }

    noise_model->combined_state[channel].num_observations +=
        noise_model->latest_state[channel].num_observations;
    equation_system_add(&noise_model->combined_state[channel].eqns,
                        &noise_model->latest_state[channel].eqns);
    if (!ar_equation_system_solve(&noise_model->combined_state[channel], is_chroma))
      return AOM_NOISE_STATUS_INTERNAL_ERROR;

    noise_strength_solver_add(
        &noise_model->combined_state[channel].strength_solver,
        &noise_model->latest_state[channel].strength_solver);

    if (!reel_aom_noise_strength_solver_solve(
            &noise_model->combined_state[channel].strength_solver)) {
      return AOM_NOISE_STATUS_INTERNAL_ERROR;
    }
  }

  return AOM_NOISE_STATUS_OK;
}

int reel_aom_noise_model_get_grain_parameters(reel_aom_noise_model_t *const noise_model,
                                         reel_aom_film_grain_t *film_grain) {
  if (noise_model->params.lag > 3) {
    return 0;
  }
  uint16_t random_seed = film_grain->random_seed;
  memset(film_grain, 0, sizeof(*film_grain));
  film_grain->random_seed = random_seed;

  film_grain->apply_grain = 1;
  film_grain->update_parameters = 1;

  film_grain->ar_coeff_lag = noise_model->params.lag;

  // Convert the scaling functions to 8 bit values
  reel_aom_noise_strength_lut_t scaling_points[3];
  if (!reel_aom_noise_strength_solver_fit_piecewise(
          &noise_model->combined_state[0].strength_solver, 14,
          scaling_points + 0)) {
    return 0;
  }
  if (!reel_aom_noise_strength_solver_fit_piecewise(
          &noise_model->combined_state[1].strength_solver, 10,
          scaling_points + 1)) {
    reel_aom_noise_strength_lut_free(scaling_points + 0);
    return 0;
  }
  if (!reel_aom_noise_strength_solver_fit_piecewise(
          &noise_model->combined_state[2].strength_solver, 10,
          scaling_points + 2)) {
    reel_aom_noise_strength_lut_free(scaling_points + 0);
    reel_aom_noise_strength_lut_free(scaling_points + 1);
    return 0;
  }

  // Both the domain and the range of the scaling functions in the film_grain
  // are normalized to 8-bit (e.g., they are implicitly scaled during grain
  // synthesis).
  const double strength_divisor = 1 << (noise_model->params.bit_depth - 8);
  double max_scaling_value = 1e-4;
  for (int c = 0; c < 3; ++c) {
    for (int i = 0; i < scaling_points[c].num_points; ++i) {
      scaling_points[c].points[i][0] =
          AOMMIN(255, scaling_points[c].points[i][0] / strength_divisor);
      scaling_points[c].points[i][1] =
          AOMMIN(255, scaling_points[c].points[i][1] / strength_divisor);
      max_scaling_value =
          AOMMAX(scaling_points[c].points[i][1], max_scaling_value);
    }
  }

  // Scaling_shift values are in the range [8,11]
  const int max_scaling_value_log2 =
      clamp((int)floor(log2(max_scaling_value) + 1), 2, 5);
  film_grain->scaling_shift = 5 + (8 - max_scaling_value_log2);

  const double scale_factor = 1 << (8 - max_scaling_value_log2);
  film_grain->num_y_points = scaling_points[0].num_points;
  film_grain->num_cb_points = scaling_points[1].num_points;
  film_grain->num_cr_points = scaling_points[2].num_points;

  int(*film_grain_scaling[3])[2] = {
    film_grain->scaling_points_y,
    film_grain->scaling_points_cb,
    film_grain->scaling_points_cr,
  };
  for (int c = 0; c < 3; c++) {
    for (int i = 0; i < scaling_points[c].num_points; ++i) {
      film_grain_scaling[c][i][0] = (int)(scaling_points[c].points[i][0] + 0.5);
      film_grain_scaling[c][i][1] = clamp(
          (int)(scale_factor * scaling_points[c].points[i][1] + 0.5), 0, 255);
    }
  }
  reel_aom_noise_strength_lut_free(scaling_points + 0);
  reel_aom_noise_strength_lut_free(scaling_points + 1);
  reel_aom_noise_strength_lut_free(scaling_points + 2);

  // Convert the ar_coeffs into 8-bit values
  const int n_coeff = noise_model->combined_state[0].eqns.n;
  double max_coeff = 1e-4, min_coeff = -1e-4;
  double y_corr[2] = { 0, 0 };
  double avg_luma_strength = 0;
  for (int c = 0; c < 3; c++) {
    reel_aom_equation_system_t *eqns = &noise_model->combined_state[c].eqns;
    for (int i = 0; i < n_coeff; ++i) {
      max_coeff = AOMMAX(max_coeff, eqns->x[i]);
      min_coeff = AOMMIN(min_coeff, eqns->x[i]);
    }
    // Since the correlation between luma/chroma was computed in an already
    // scaled space, we adjust it in the un-scaled space.
    reel_aom_noise_strength_solver_t *solver =
        &noise_model->combined_state[c].strength_solver;
    // Compute a weighted average of the strength for the channel.
    double average_strength = 0, total_weight = 0;
    for (int i = 0; i < solver->eqns.n; ++i) {
      double w = 0;
      for (int j = 0; j < solver->eqns.n; ++j) {
        w += solver->eqns.A[i * solver->eqns.n + j];
      }
      w = sqrt(w);
      average_strength += solver->eqns.x[i] * w;
      total_weight += w;
    }
    if (total_weight == 0)
      average_strength = 1;
    else
      average_strength /= total_weight;
    if (c == 0) {
      avg_luma_strength = average_strength;
    } else {
      y_corr[c - 1] =
          average_strength > 1e-6
              ? avg_luma_strength * eqns->x[n_coeff] / average_strength
              : 0;
      max_coeff = AOMMAX(max_coeff, y_corr[c - 1]);
      min_coeff = AOMMIN(min_coeff, y_corr[c - 1]);
    }
  }
  // Shift value: AR coeffs range (values 6-9)
  // 6: [-2, 2),  7: [-1, 1), 8: [-0.5, 0.5), 9: [-0.25, 0.25)
  film_grain->ar_coeff_shift =
      clamp(7 - (int)AOMMAX(1 + floor(log2(max_coeff)), ceil(log2(-min_coeff))),
            6, 9);
  double scale_ar_coeff = 1 << film_grain->ar_coeff_shift;
  int *ar_coeffs[3] = {
    film_grain->ar_coeffs_y,
    film_grain->ar_coeffs_cb,
    film_grain->ar_coeffs_cr,
  };
  for (int c = 0; c < 3; ++c) {
    reel_aom_equation_system_t *eqns = &noise_model->combined_state[c].eqns;
    for (int i = 0; i < n_coeff; ++i) {
      ar_coeffs[c][i] =
          clamp((int)round(scale_ar_coeff * eqns->x[i]), -128, 127);
    }
    if (c > 0) {
      ar_coeffs[c][n_coeff] =
          clamp((int)round(scale_ar_coeff * y_corr[c - 1]), -128, 127);
    }
  }

  // At the moment, the noise modeling code assumes that the chroma scaling
  // functions are a function of luma.
  film_grain->cb_mult = 128;       // 8 bits
  film_grain->cb_luma_mult = 192;  // 8 bits
  film_grain->cb_offset = 256;     // 9 bits

  film_grain->cr_mult = 128;       // 8 bits
  film_grain->cr_luma_mult = 192;  // 8 bits
  film_grain->cr_offset = 256;     // 9 bits

  film_grain->chroma_scaling_from_luma = 0;
  film_grain->grain_scale_shift = 0;
  film_grain->overlap_flag = 1;
  return 1;
}

