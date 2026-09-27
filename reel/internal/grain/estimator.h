#ifndef REEL_GRAIN_ESTIMATOR_H
#define REEL_GRAIN_ESTIMATOR_H

#include <stdint.h>
#include "grain_params.h"

// Four frames from each of twelve gate chunks, with equal patch quotas.
#define REEL_GRAIN_MAX_FRAMES 48
#define REEL_GRAIN_PATCHES_PER_FRAME 16

typedef struct reel_grain_estimator reel_grain_estimator;
reel_grain_estimator *reel_grain_create(void);
void reel_grain_free(reel_grain_estimator *estimator);
// The input is packed YUV420P10LE. No input pointer survives this call.
int reel_grain_sample(reel_grain_estimator *estimator, const uint8_t *source,
                      const uint8_t *denoised, int width, int height, int phase);
int reel_grain_fit(reel_grain_estimator *estimator, reel_aom_film_grain_t *grain);

#endif
