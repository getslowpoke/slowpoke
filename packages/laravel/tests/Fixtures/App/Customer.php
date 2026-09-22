<?php

namespace Slowpoke\Laravel\Tests\Fixtures\App;

use Illuminate\Database\Eloquent\Model;

class Customer extends Model
{
    public $timestamps = false;
    protected $guarded = [];
}
