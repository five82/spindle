/*
 * Copyright (c) 2016, Alliance for Open Media. All rights reserved.
 *
 * This source code is subject to the terms of the BSD 2 Clause License and
 * the Alliance for Open Media Patent License 1.0. If the BSD 2 Clause License
 * was not distributed with this source code in the LICENSE file, you can
 * obtain it at www.aomedia.org/license/software. If the Alliance for Open
 * Media Patent License 1.0 was not distributed with this source code in the
 * PATENTS file, you can obtain it at www.aomedia.org/license/patent.
 */

/*!\file
 * \brief Describes film grain parameters
 *
 */
#ifndef AOM_AOM_DSP_GRAIN_PARAMS_H_
#define AOM_AOM_DSP_GRAIN_PARAMS_H_

#ifdef __cplusplus
extern "C" {
#endif

#include <stdint.h>
#include <string.h>


/*!\brief Structure containing film grain synthesis parameters for a frame
 *
 * This structure contains input parameters for film grain synthesis
 */
typedef struct {
  // This structure is compared element-by-element in the function
  // reel_aom_check_grain_params_equiv: this function must be updated if any changes
  // are made to this structure.
  int apply_grain;

  int update_parameters;

  // 8 bit values
  int scaling_points_y[14][2];
  int num_y_points;  // value: 0..14

  // 8 bit values
  int scaling_points_cb[10][2];
  int num_cb_points;  // value: 0..10

  // 8 bit values
  int scaling_points_cr[10][2];
  int num_cr_points;  // value: 0..10

  int scaling_shift;  // values : 8..11

  int ar_coeff_lag;  // values:  0..3

  // 8 bit values
  int ar_coeffs_y[24];
  int ar_coeffs_cb[25];
  int ar_coeffs_cr[25];

  // Shift value: AR coeffs range
  // 6: [-2, 2)
  // 7: [-1, 1)
  // 8: [-0.5, 0.5)
  // 9: [-0.25, 0.25)
  int ar_coeff_shift;  // values : 6..9

  int cb_mult;       // 8 bits
  int cb_luma_mult;  // 8 bits
  int cb_offset;     // 9 bits

  int cr_mult;       // 8 bits
  int cr_luma_mult;  // 8 bits
  int cr_offset;     // 9 bits

  int overlap_flag;

  int clip_to_restricted_range;

  unsigned int bit_depth;  // video bit depth

  int chroma_scaling_from_luma;

  int grain_scale_shift;

  uint16_t random_seed;
  // This structure is compared element-by-element in the function
  // reel_aom_check_grain_params_equiv: this function must be updated if any changes
  // are made to this structure.
} reel_aom_film_grain_t;


#ifdef __cplusplus
}
#endif
#endif
